<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->
# Karta Quickstart

> **What is Karta?** A Go library + Kubernetes CRD that gives platform controllers a single, uniform API over any workload type — JobSet, LeaderWorkerSet, PyTorchJob, RayCluster, and more. You describe each CRD's structure once in a YAML definition; your controller code never hard-codes per-type paths again. This example runs entirely offline — no cluster required.

## The problem

A scheduler plugin needs to inject `schedulerName: kai-scheduler` into every pod of every workload it manages:

```go
// Without Karta — grows with every new workload type you support
switch workload.GetKind() {
case "JobSet":
    for i := range jobset.Spec.ReplicatedJobs {
        jobset.Spec.ReplicatedJobs[i].Template.Spec.Template.Spec.SchedulerName = scheduler
    }
case "LeaderWorkerSet":
    lws.Spec.LeaderWorkerTemplate.LeaderTemplate.Spec.SchedulerName = scheduler
    lws.Spec.LeaderWorkerTemplate.WorkerTemplate.Spec.SchedulerName = scheduler
// case "PyTorchJob": ...
// case "RayCluster": ...
}
```

With Karta:

```go
// With Karta — identical for every workload type
for _, comp := range children {
    pts, _ := comp.GetPodTemplateSpec(ctx)
    for id, t := range pts {
        t.Spec.SchedulerName = scheduler
        pts[id] = t
    }
    comp.UpdatePodTemplateSpec(ctx, pts)
}
```

The same code in `main.go` runs over two completely different CRD types — **no `switch` on CRD kind anywhere**.

## Run it

From the `docs/examples/quickstart` directory:

This example is a standalone module with its own `go.work`, so the commands
below run as written. Its local `replace` directive selects the repository
library.

```bash
# Default — injects kai-scheduler
go run .

# Use a different scheduler
go run . --scheduler volcano

# Also print the full mutated CRD YAML
go run . --scheduler my-scheduler --print-mutated

# Help
go run . --help
```

Default expected output:

```text
══════════════════════════════════════════
  JobSet  (scheduler: kai-scheduler)
══════════════════════════════════════════

=== Workload status ===
  Karta workload status: Running

=== Component replica counts ===
  replicatedjob[leader]        replicas=1
  replicatedjob[workers]       replicas=8

=== Resource requests per component ===
  replicatedjob[leader]        container=training     cpu=4        memory=32Gi       gpu=1
  replicatedjob[workers]       container=training     cpu=8        memory=64Gi       gpu=4

=== Injecting scheduler "kai-scheduler" + label ===
  Injected into "replicatedjob" (2 instances)

=== Verification ===
  replicatedjob[leader]        schedulerName="kai-scheduler"      managed-by="karta"
  replicatedjob[workers]       schedulerName="kai-scheduler"      managed-by="karta"

  → In a real controller: k8sClient.Update(ctx, updated)

══════════════════════════════════════════
  LeaderWorkerSet  (scheduler: kai-scheduler)
══════════════════════════════════════════

=== Workload status ===
  Karta workload status: Initializing

=== Component replica counts ===
  group (virtual)              replicas=4
  leader                       replicas=3
  worker                       replicas=9

=== Resource requests per component ===
  leader                       container=nginx2       cpu=500m     memory=512Mi      gpu=<none>
  worker                       container=nginx        cpu=200m     memory=256Mi      gpu=<none>

=== Injecting scheduler "kai-scheduler" + label ===
  Injected into "leader" (1 instance)
  Injected into "worker" (1 instance)

=== Verification ===
  leader                       schedulerName="kai-scheduler"      managed-by="karta"
  worker                       schedulerName="kai-scheduler"      managed-by="karta"

  → In a real controller: k8sClient.Update(ctx, updated)
```

## What the example does

| Step | How | What it shows |
|------|-----|---------------|
| 1 | `tree.Build()` → `wt.Status.Phases` | Unified `Running/Initializing/Failed` — no per-CRD condition parsing |
| 2 | `tree.Build()` → walk `wt.Children` | Replica counts regardless of where the CRD stores them; LWS worker total computed via a JQ formula; virtual components labelled |
| 3 | `inst.ExtractedInstance.PodTemplateSpec` | Resource requests per container — GPUs for JobSet, CPU for LWS — same traversal, different CRDs |
| 4 | `comp.GetPodTemplateSpec` → mutate → `UpdatePodTemplateSpec` | Inject scheduler name and a pod label in one pass via real `corev1` types |
| 5 | `comp.GetPodTemplateSpec` read-back + `factory.GetResource()` | Confirm both mutations landed at the right paths; retrieve the object for `k8sClient.Update` |

## File layout

| File | Purpose |
|------|---------|
| `main.go` | Example code — runs identically for both workload types |
| `jobset.yaml` | Sample `JobSet` workload (leader × 1, worker × 4) |
| `lws.yaml` | Sample `LeaderWorkerSet` workload (2 groups × 4 pods) |
| `docs/catalog/jobset-x-k8s-io-jobset-v1alpha2.yaml` | Karta definition for JobSet (loaded at runtime) |
| `docs/catalog/leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml` | Karta definition for LeaderWorkerSet (loaded at runtime) |

## How Karta works

A Karta YAML describes the structure of a CRD using JQ paths:

```yaml
# JobSet child component — one entry per replicatedJob, identified by name
childComponents:
  - name: replicatedjob
    specDefinition:
      podTemplateSpecPath: .spec.replicatedJobs[].template.spec.template
    scaleDefinition:
      replicasPath: .spec.replicatedJobs[].replicas
    instanceIdPath: .spec.replicatedJobs[].name

# LWS worker total computed directly in JQ
  - name: worker
    specDefinition:
      podTemplateSpecPath: .spec.leaderWorkerTemplate.workerTemplate
    scaleDefinition:
      replicasPath: (.spec.replicas // 1) * ((.spec.leaderWorkerTemplate.size // 1) - 1)
```

Your Go code never references these paths directly — Karta handles the navigation. Adding support for a new CRD means writing a new YAML definition; existing code is untouched.

## Next steps

- [Technical Guide](../../Technical%20Guide.md) — full Karta specification
- [catalog](../../catalog/) — ready-made definitions for PyTorchJob, RayCluster, MPIJob, KServe, and more
- [resource](../../../pkg/resource/) — full Component API (suspend/resume, fragmented pod specs, pod querier)
- [tree](../../../pkg/tree/) — WorkloadTree for inspecting the component hierarchy of live workloads
- [instructions](../../../pkg/instructions/) — gang scheduling and `StructureSummary` for scheduler integrations
