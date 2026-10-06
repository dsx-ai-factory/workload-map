<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta e2e

Records real workloads on a live cluster and keeps the recordings as fixtures, so Karta
can be checked against real data.

- recorder/: the record engine. Drives a workload through a declared flow and saves every
  CR it passes through. See recorder/README.md.
- flows/: one Ginkgo file per workload type. Each declares the journey and records it.
- recorded_data/: the saved fixtures, one YAML per flow, filed under
  operator/version/kartaName/flow.yaml.

## Record

```sh
make e2e-up                                  # provision kind + operators (hack/e2e)
make record-e2e                              # record against the current cluster
make record-e2e WORKLOADS="pod"              # one workload type
make record-e2e WORKLOADS="pod" FLOW="running" # one flow of one type
KUBECONFIG=/path/to/your.kubeconfig \
  make record-e2e WORKLOADS="pod"            # any cluster, not only kind
```

record-e2e records whatever cluster kubectl points at - kind from e2e-up or any cluster
with Karta installed. FLOW without WORKLOADS matches that flow name across all types.
Other knobs: CLUSTER_NAME=<name> targets a named kind cluster from e2e-up,
E2E_TIMEOUT=10m caps the whole run, E2E_LABELS passes a raw ginkgo label expression.

## Test

```sh
make test-lib            # library unit tests, plus the recorder unit tests (offline, no cluster)
make verify-recordings   # fail if any recorded fixture ended with succeeded: false
```

An offline replay suite that feeds recorded_data back through Karta follows in the next
slice.
