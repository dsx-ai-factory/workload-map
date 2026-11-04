<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->
# Exporter E2E

Requires Docker with at least four CPUs, Kind, kubectl, Helm and Go.
From the repository root:

```sh
make exporter-e2e-up
make test-exporter-e2e
make exporter-e2e-down
```

The harness creates `dsx-exporter-e2e`, builds and loads the exporter image,
and installs two Helm releases with separate namespace selectors. It also installs
JobSet and LeaderWorkerSet at the versions pinned in `hack/e2e/global.env`.
The test exporters request 100m CPU each to fit alongside both operators. Their kubeconfig
and failure logs stay in `.artifacts/exporter-e2e/<cluster-name>`; the current kubectl context
is not changed. Set `EXPORTER_E2E_CLUSTER` to choose another isolated cluster name.

The suite uses Ginkgo/Gomega under `<module>/test/e2e`, like the operator.
Start with the file for the behavior being tested. Each scenario contains its
own `It` body.

| File | What it contains |
| --- | --- |
| `workloads_test.go` | Pod and Job lifecycles, dynamic Karta definitions, permission failure and recovery. |
| `operators_test.go` | JobSet components and completion; LeaderWorkerSet groups and scale-down. |
| `namespace_test.go` | Moving a populated namespace between exporter shards. |
| `suite_test.go` | Suite entry point, setup, cleanup, Kubernetes commands and metric helpers. |

All eight specs run serially because they share exporter installations. The five
native workload specs also run in order: permission coverage reuses the lifecycle
Job. Shared namespaces are retained if any spec fails.

Tests sample metrics every 250ms. Controlled workload stages remain in place
until their metrics have been observed. CI runs the suite for changes to the
exporter, SDK, catalog, chart, E2E harness or build configuration. The CI job has
a 15-minute limit including tool setup, cluster setup, tests and cleanup.

The suite covers Pod and Job lifecycle gates, a root without Pods, live Karta
updates and evaluation recovery, namespace selector handoff, and permission
failure recovery. Permission loss in one namespace must not invalidate Job or
Pod metrics in another namespace that remains authorized. A new Pod with an
unreadable Job owner emits discovery health without guessed workload metrics.
After permission recovery, the same Pod UID must resolve to the same Job UID.
Namespace handoff checks all four metric families after a populated namespace
moves between exporters.

The real-operator cases use the bundled catalog definitions. JobSet runs two
worker Jobs and one evaluator Job. The suite checks each Pod's Job owner and
JobSet root UID, component instance, completion, and deletion cleanup.
LeaderWorkerSet runs two groups of one leader and one worker, then scales to one
group. The retained Pod UIDs must remain unchanged and the removed UIDs must
leave the metrics. Root and Pod labels also verify custom-label source selection.

Pod and Job init containers wait on files written by the test. JobSet completion
uses the same gate. Operator status assertions first read the live controller
status and compare all nine phase gauges in one scrape. Catalog phases can
overlap. The synthetic Deployment Karta is used only for definition updates and
evaluation failure recovery; it does not establish operator lifecycle behavior.
