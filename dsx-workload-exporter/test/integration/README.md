<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Exporter consistency test

`exporter/consistency_test.go` checks that exported metrics follow the API objects.
For example, deleting a ReplicaSet must mark its remaining Pods invalid without
waiting for another Pod update. A replacement ReplicaSet with the same name must
not satisfy references to the old UID.

The fixture uses two Deployments, one ReplicaSet and three Pods. It checks all four
metric families after reparenting, deletion, replacement and recovery. Expectations
come from API lists, not the exporter's store. Missing owners retain the last
confirmed attribution, marked invalid. Definitions are disabled to keep this test
focused on ownership; status evaluation has separate tests.

The test uses envtest: a local API server and etcd, without kubelets or controllers.
It runs in the existing integration suite through `make check`. No Docker or cluster
installation is needed. Run only this guardrail from the repository root:

```sh
make envtest
export KUBEBUILDER_ASSETS="$(bin/setup-envtest-release-0.23 use 1.31.0 --bin-dir bin -p path)"
cd dsx-workload-exporter/test/integration
go test -race ./exporter -ginkgo.focus='Metric consistency' -count=1 -timeout=2m
```

The scenario has a 90-second deadline. The command has a two-minute test timeout;
first-time tool downloads and compilation are additional. This checks data
correctness, not throughput or performance under load.
