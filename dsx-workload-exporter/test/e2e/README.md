<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->
# Exporter E2E

Requires Docker, Kind, kubectl, Helm and Go. From the repository root:

```sh
make exporter-e2e-up
make test-exporter-e2e
make exporter-e2e-down
```

The harness creates `dsx-exporter-e2e`, builds and loads the exporter image,
and installs two Helm releases with separate namespace selectors. It also installs
JobSet and LeaderWorkerSet at the versions pinned in `hack/e2e/global.env`. Its kubeconfig
and failure logs stay in `.artifacts/exporter-e2e/<cluster-name>`; the current kubectl context
is not changed. Set `EXPORTER_E2E_CLUSTER` to choose another isolated cluster name.

Tests sample metrics every 250ms. Controlled workload stages remain in place
until their metrics have been observed. CI runs the suite for changes to the
exporter, SDK, catalog, chart, E2E harness or build configuration. The CI job has
a 15-minute limit including tool setup, cluster setup, tests and cleanup.

The suite covers Pod and Job lifecycle gates, a root without Pods, live Karta
updates and evaluation recovery, namespace selector handoff, and permission
failure recovery. Namespace handoff checks all four metric families after a
populated namespace moves between exporters.

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
