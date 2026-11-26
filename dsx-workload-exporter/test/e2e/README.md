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
and installs two Helm releases with separate namespace selectors. The HTTPS spec
installs a third, `exporter-e2e-tls`, with a certificate it generates. The OTLP
spec installs one per protocol, pushing to an OpenTelemetry Collector in
`exporter-otlp`. The harness also installs JobSet and LeaderWorkerSet at the
versions pinned in `hack/e2e/global.env`, and loads the busybox and Collector
images the fixtures pin. The two shards request 100m CPU each, and the releases
the specs install 25m and 64Mi of memory, to fit alongside both operators. Their kubeconfig
and failure logs stay in `.artifacts/exporter-e2e/<cluster-name>`; the current kubectl context
is not changed. Set `EXPORTER_E2E_CLUSTER` to choose another isolated cluster name.

The suite uses Ginkgo/Gomega under `<module>/test/e2e`, like the operator.
Start with the file for the behavior being tested. Each scenario contains its
own `It` body. Tests submit a workload or change, wait for the expected state,
then verify the exported metrics. `By` steps describe these actions in the test
log, including successful runs.

| File | What it contains |
| --- | --- |
| `pod_test.go` | Standalone Pod lifecycle, failure and metric cleanup. |
| `job_test.go` | Job lifecycle, permission failure and recovery. |
| `deployment_test.go` | Zero-Pod Deployment, live Karta changes and ReplicaSet ownership. |
| `jobset_test.go` | JobSet components, completion and metric cleanup. |
| `leaderworkerset_test.go` | LeaderWorkerSet groups and scale-down. |
| `multiple_exporters_test.go` | Moving a populated namespace between exporter shards. |
| `tls_test.go` | Scraping over HTTPS with a generated CA, and rejecting an unrelated CA. |
| `otlp_test.go` | OTLP push over both protocols, and a Collector that scrapes and forwards. |
| `suite_test.go` | Suite entry point, setup, cleanup, Kubernetes commands and metric helpers. |
| `testdata/*.yaml` | Workload, namespace, Karta and Collector fixtures loaded by `submitWorkload`. |

All twelve specs run serially because they share exporter installations. The two
Job specs run in order: permission coverage reuses the completed lifecycle Job.
Pod lifecycle and failure also run in order. Workload groups can run in any order.
Shared namespaces are retained if any spec fails.

Tests fetch `/metrics` through the exporter Service proxy and parse it with
`expfmt`. The HTTPS spec port-forwards instead, because the proxy does not verify
the exporter's certificate. The OTLP spec reads the batches the Collector writes
to a file per pipeline, so it can check single exports. Checks compare metric names, labels and gauge values. Retries wait 250ms
between requests. Controlled workload stages remain in place until their metrics
have been observed. CI runs the suite for changes to the exporter, SDK, catalog,
chart, E2E harness or build configuration. The CI job has a 15-minute limit
including tool setup, cluster setup, tests and cleanup.

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
status and compare all seven default phase gauges in one scrape. Catalog phases can
overlap. The synthetic Deployment Karta is used only for definition updates and
evaluation failure recovery; it does not establish operator lifecycle behavior.
