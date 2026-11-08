<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Kueue Workload

Kueue creates a Workload to request quota for a Job, Pod group or another
supported workload. Karta reads its admission, readiness and activity. For
example, this Workload fragment reports that its Pods are admitted and ready:

```yaml
apiVersion: kueue.x-k8s.io/v1beta2
kind: Workload
metadata:
  name: training
spec:
  active: true
status:
  conditions:
    - type: Admitted
      status: "True"
    - type: PodsReady
      status: "True"
```

Karta returns `Running` for these fields. Admission without `PodsReady=True`
returns `Undefined`. This is the Workload's readiness, not the status of a task
running inside long-lived Pods.

## Install the definition

The built-in catalog supports `kueue.x-k8s.io/v1beta2/Workload`. The local e2e
environment pins Kueue v0.20.5, whose CRD does not serve v1beta1. The CLI loads
the catalog automatically.

The Helm chart installs Karta's controller and CRD. Apply the YAML when using
cluster Karta definitions:

```sh
kubectl apply -f docs/catalog/kueue-x-k8s-io-workload-v1beta2.yaml
kubectl wait karta/kueue-x-k8s-io-workload-v1beta2 --for=condition=Ready --timeout=60s
```

## Status and operations

An omitted `spec.active` means active. In the table, unfinished means there is
no `Finished=True` condition.

| Workload fields | Karta status |
|---|---|
| Active, `Admitted=False`, unfinished | Initializing |
| Active, `Admitted=True`, `PodsReady=True`, no `Evicted=True`, `WaitingForReplacementPods=True` or `Finished=True` | Running |
| `active=false`, `QuotaReserved=True`, unfinished | Suspending |
| `active=false`, no `QuotaReserved=True`, unfinished | Suspended |
| `Finished=True`, reason `Failed` or `FailedToStart` | Failed |
| Any other combination, including `Finished=True` with another reason | Undefined |

Kueue reports `PodsReady` when `waitForPodsReady` or workload-level readiness
reporting is enabled. Old readiness conditions during eviction, replacement,
deactivation or completion do not imply `Running`.

`Succeeded` does not prove every Pod succeeded. Kueue can use this reason for
a finished Pod group containing failed Pods. `OutOfSync` describes a prebuilt
Workload that differs from its owning job. `OwnerNotFound` marks orphan cleanup
and can follow intentional owner deletion. Neither proves execution failed.
Karta preserves the original conditions for callers that need those details.

This definition reads status only. It exposes no suspend, resume, scale or Pod
template mutations. Setting `active=false` through Kueue can delete bare group
Pods, and reactivation cannot recreate them without an owning controller.
Workload PodSets describe admission requests, not the execution templates to
edit when changing an owning job.

## Pod ownership

Workload objects do not own the job's Pods. The CLI cannot discover those Pods
by following owner references from a Workload. Use the owning Job or workload
definition to inspect its Pod tree.

## Local verification

```sh
make e2e-up CLUSTER_NAME=karta-kueue WORKLOADS=kueue
make record-e2e CLUSTER_NAME=karta-kueue WORKLOADS=kueue
make test-replay
make verify-recordings
```

The status interpretation follows the Kueue v0.20.5
[job reconciler](https://github.com/kubernetes-sigs/kueue/blob/v0.20.5/pkg/controller/jobframework/reconciler.go),
[Pod integration](https://github.com/kubernetes-sigs/kueue/blob/v0.20.5/pkg/controller/jobs/pod/pod_controller.go)
and [orphan cleanup](https://github.com/kubernetes-sigs/kueue/blob/v0.20.5/pkg/workload/workload.go).
