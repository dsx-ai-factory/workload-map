<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Exporter load test

Run from the repository root:

```sh
make test-exporter-load
EXPORTER_LOAD_WORKLOADS=1000 EXPORTER_LOAD_DURATION=30s make test-exporter-load
make test-exporter-load EXPORTER_LOAD_COUNT=10
```

The Ginkgo suite starts a local Kubernetes API server and etcd through envtest, then runs
the exporter against them. Writers continuously update workload and Pod objects
while one HTTP client attempts a scrape every 100ms. Slow scrapes reduce the
actual scrape rate. After writes stop, the test waits for the final identities
and phases, checks timestamps and collection health,
and verifies that deleting objects removes their metrics.

The defaults are 100 workload roots with 200 Pods, four writers and at least
five seconds of updates. Each writer finishes a complete round, so updates can
last longer than the requested duration.
`EXPORTER_LOAD_WORKLOADS`, `EXPORTER_LOAD_WRITERS` and `EXPORTER_LOAD_DURATION`
change that load. `EXPORTER_LOAD_COUNT` repeats the suite with a fresh API
server. `EXPORTER_LOAD_TIMEOUT` defaults to `10m` for all repetitions together;
increase it for long durations or many repetitions.

The log reports completed API writes, time spent scraping and validating metrics,
and Go heap usage. `scrape_max` includes the request, parsing and validation.
The exporter and test share that heap; these are not isolated exporter CPU or
memory measurements. No machine-dependent throughput limits are enforced.

This exercises real API requests, watches and exporter metrics. Envtest has no
scheduler, kubelets or workload controllers. The test supplies objects and
status changes directly, so it does not measure Pod startup or cluster capacity.
It runs when `EXPORTER_LOAD=1` is set, including through the load target.
Ordinary tests compile it but skip the run.

The dedicated [Exporter load workflow](../../../../.github/workflows/exporter-load.yaml)
runs three times for changes under `dsx-workload-exporter/` or to the workflow
itself. Its `exporter-load-results` artifact contains the test output.
