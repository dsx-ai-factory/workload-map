<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Sample index

Pick the definition whose shape is closest to the target workload, copy it, and
adapt the GVK, paths, and status mapping.

The table names each definition by its catalog file, but the file is only one way
to reach it. The `karta` CLI embeds the same definitions, so prefer it when it is
on PATH and fetch the file only as a fallback:

```bash
karta definitions <name> -o yaml                       # CLI, no cluster needed
curl -sL "$KARTA_RAW/docs/catalog/<name>.yaml"         # fallback; see SKILL.md for $KARTA_RAW
```

`karta definitions` with no arguments lists every row below with a COMPONENTS
column naming its component tree, which is often a faster way to match a shape
than reading the table.

`docs/catalog/` is generated from the typed Go definitions in
`pkg/catalog/kartas/` by `make generate-samples`, and `make validate` fails CI on
drift. Read it freely, but never edit a file there in place: on the standalone
path the copy belongs outside the repository, and on the built-in path the edit
belongs in the Go source.

## How to choose

Answer these, then choose the most specific matching row in the table below (the
one naming the most of your workload's traits). When several rows match, a
multi-instance or nested pattern (for example Ray worker groups needing
`instanceIdPath`) takes precedence over a generic role-based row.

1. Does the workload own other resources, or is it a single object with pods
   defined in its own spec?
2. Where does the pod template live: a full template, a bare pod spec, or
   scattered fields?
3. Does it report status through conditions, a phase string, or status fields?
4. Does any component hold several specs (worker groups, services, replicated
   jobs) that need per-instance identity?
5. Are there nested layers of ownership (a group that owns a leader and workers)?

## Decision table

| Workload shape | Closest sample | Why |
|---|---|---|
| Single Job, full pod template, status via conditions plus a computed expression | `docs/catalog/batch-job-v1.yaml` | `podTemplateSpecPath: .spec.template`, `byConditions` plus `byExpression`, suspend via `.spec.suspend`. |
| CronJob wrapping a Job template, pod template nested under `jobTemplate` | `docs/catalog/batch-cronjob-v1.yaml` | One child `Job`, `podTemplateSpecPath: .spec.jobTemplate.spec.template`, expression-only status, suspend. |
| Deployment or other controller that generates ReplicaSets | `docs/catalog/apps-deployment-v1.yaml`, `docs/catalog/serving-knative-dev-service-v1.yaml` | Root plus a generated child; child carries the pod template. |
| Role-based distributed job (master and worker, launcher and worker) with pods as children | `docs/catalog/kubeflow-org-pytorchjob-v1.yaml`, `docs/catalog/kubeflow-org-mpijob-v2beta1.yaml` | Children are `Pod` kind, one per role, each with a `componentTypeSelector` on a role label. The label key is operator-specific: PyTorchJob uses `training.kubeflow.org/replica-type`, MPIJob uses `training.kubeflow.org/job-role`. Verify the real label before copying. |
| Several worker groups under one role that need per-group identity | `docs/catalog/ray-io-raycluster-v1.yaml`, `docs/catalog/ray-io-rayjob-v1.yaml`, `docs/catalog/ray-io-rayservice-v1.yaml` | Worker component uses `instanceIdPath` on `workerGroupSpecs[].groupName` paired with a `componentInstanceSelector`. |
| Replicated jobs, each a named template instance | `docs/catalog/jobset-x-k8s-io-jobset-v1alpha2.yaml` | `replicatedjob` child with `instanceIdPath: .spec.replicatedJobs[].name` and a `componentInstanceSelector`. |
| Nested ownership with identical replicated sub-structures (a group that owns a leader and workers) | `docs/catalog/leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml` | `group` child owns `leader` and `worker`; `replicaSelector` on the group; `componentTypeSelector` distinguishes leader from worker. |
| Fields scattered across the spec, status via a phase string | `docs/catalog/nvidia-com-dynamographdeployment-v1alpha1.yaml`, `docs/catalog/nvidia-com-dynamographdeployment-v1beta1.yaml`, `docs/catalog/apps-nvidia-com-nimservice-v1alpha1.yaml`, `docs/catalog/apps-nvidia-com-nimcache-v1alpha1.yaml` | `fragmentedPodSpecDefinition` with per-field paths; `phaseDefinition` plus `byPhase` mappings. |
| Status reported through both a phase and conditions | `docs/catalog/milvus-io-milvus-v1beta1.yaml` | Declares both `phaseDefinition` and `conditionsDefinition`; maps statuses `byPhase`. |
| Multi-service inference, each service its own component | `docs/catalog/serving-kserve-io-inferenceservice-v1beta1.yaml` | Predictor and transformer children mix `fragmentedPodSpecDefinition` and `podSpecPath` plus `metadataPath`; `componentTypeSelector` per service. |
| Nested pod cliques and scaling groups | `docs/catalog/grove-io-podcliqueset-v1alpha1.yaml` | Multiple multi-instance children (`clique`, `scalinggroup`) each with `instanceIdPath` plus instance and replica selectors. This CRD has no aggregate phase, so status is mapped with `byExpression` over replica counts, not `byPhase`. |
| Controller-managed replicas with stable identity, status from counters rather than conditions | `docs/catalog/apps-statefulset-v1.yaml` | No conditions at all: every status is a `byExpression` over `.status` counters against `.spec.replicas`, including a scale-down rule that fires while extra pods drain. |
| A bare Pod, or the simplest possible single-component definition | `docs/catalog/core-pod-v1.yaml` | The one kind allowed to omit the group. Useful as a minimal skeleton when no row above is close. |


## Pattern quick reference

- Single full pod template: `podTemplateSpecPath`. See `batch-job-v1.yaml`.
- Bare pod spec with separate metadata: `podSpecPath` plus `metadataPath`. See
  the transformer child in `serving-kserve-io-inferenceservice-v1beta1.yaml`.
- Scattered fields: `fragmentedPodSpecDefinition`. See
  `nvidia-com-dynamographdeployment-v1beta1.yaml`.
- Conditions status: `conditionsDefinition` plus `byConditions`. See
  `ray-io-rayservice-v1.yaml`, which also uses `reason` to separate degraded from
  failed.
- Phase status: `phaseDefinition` plus `byPhase`. See `ray-io-raycluster-v1.yaml`.
- Expression status: `byExpression`. See `batch-job-v1.yaml`.
- Per-instance identity: `instanceIdPath` plus `componentInstanceSelector`. See
  `ray-io-raycluster-v1.yaml`.
- Replica identity within identical sub-structures: `replicaSelector`. See
  `leaderworkerset-x-k8s-io-leaderworkerset-v1.yaml`.

## Finding a real CR to test against

`test/e2e/recorded_data/<operator>/<k8s-version>/<karta-name>/<flow>.yaml` holds
CRs recorded from live clusters for 17 of the 20 definitions in the table above.
`ray-io-rayservice-v1`, `apps-nvidia-com-nimcache-v1alpha1`, and
`nvidia-com-dynamographdeployment-v1beta1` have no recordings at all. The
`karta-name` segment is the catalog file's stem, so the recordings for a row sit
under the same name as its definition; the rest of the path does not follow from
the row.

These live in the repository, not in the CLI. Read them from a clone, or fetch
individual files at the pinned commit; step 1 of `SKILL.md` has both commands.
Running a definition against one still needs a clone, since that is
`hack/karta-verify`.

Do not assemble the path from a row here. The `<operator>` segment is the e2e
suite's own name and frequently differs from the definition (`kuberay`, `lws`,
`kubeflow`, `nim`), `<k8s-version>` is unpredictable, and the flows present vary
per type. `SKILL.md` step 1 lists before picking, which is the only reliable
order.

When the target type is not recorded, the nearest row's recordings are still
worth reading: they show what the controller genuinely writes to `.status`,
which a CRD schema does not. Step 1 of `SKILL.md` has the extraction command.
