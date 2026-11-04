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
and installs two Helm releases with separate namespace selectors. Its kubeconfig
and failure logs stay in `.artifacts/exporter-e2e/<cluster-name>`; the current kubectl context
is not changed. Set `EXPORTER_E2E_CLUSTER` to choose another isolated cluster name.

Tests sample metrics every 250ms. Controlled workload stages remain in place
until their metrics have been observed. CI runs the suite for changes to the
exporter, SDK, catalog, chart, E2E harness or build configuration.
