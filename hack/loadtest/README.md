<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Exporter scale test

This scale test measures the DSX workload exporter with 10k to 150k Pods on one kind
cluster. KWOK runs fake nodes and sets Pod status, so no container runs. The real
Deployment, Job, JobSet and LeaderWorkerSet controllers create the owner chains.
A kube-burner Job in the cluster creates the workload roots. Prometheus records
the exporter and the control plane.

## Quick start

```sh
make loadtest-bench                 # no cluster: Store and /metrics cost at 10k, 50k, 150k Pods
make loadtest-up TIER=10k           # kind, KWOK, Prometheus, JobSet, LWS, exporter
make loadtest-run TIER=10k          # all phases, then the budget report
make loadtest-down
```

The cluster uses its own kubeconfig, `~/.kube/kind-karta-load.kubeconfig`. Your
current kubectl context does not change.

Local kind needs approximately 16 GB of memory for Docker at the 10k tier. With the
Docker Desktop default of 8 GB, the API server stops answering during create.

## Existing cluster

`CLUSTER_MODE=existing` uses the current kubectl context, for example a kind cluster
on a VM. The scripts copy that context to `~/.kube/loadtest-<context>.kubeconfig`,
so a context switch in another terminal does not move a run.

```sh
make loadtest-image CLUSTER_MODE=existing        # build for linux/amd64, import into each real node via kubectl
make loadtest-up CLUSTER_MODE=existing TIER=50k
make loadtest-run CLUSTER_MODE=existing TIER=50k
make loadtest-down CLUSTER_MODE=existing         # removes only what the scale test added
```

If the cluster already runs a Prometheus operator, up.sh reuses it at its chart
version and adds only Prometheus and kube-state-metrics, as the release
`loadtest` in `loadtest-monitoring`. That Prometheus scrapes only ServiceMonitors
labeled `release=loadtest`.

## Tiers

The tier counts Pods, not workloads. 150k is the upstream limit for one cluster.

| Tier | Pods | KWOK nodes | Namespaces | Exporter apiQPS/burst/workers | Cluster |
| --- | --- | --- | --- | --- | --- |
| 10k | 10,000 | 50 | 20 | 20/40/4 | kind |
| 50k | 50,000 | 250 | 100 | 50/100/8 | kind, 16 GB or more for Docker |
| 150k | 150,000 | 750 | 300 | 100/200/16 | dedicated control plane |

`TIER=custom` takes `SCALE_PODS` and `SCALE_NODES`. Run `make loadtest-up` again
after you change the tier, so the node count and exporter limits follow.

## Workload mix

| Type | Share of Pods | Owner chain |
| --- | --- | --- |
| Deployment | 35% | Pod, ReplicaSet, Deployment |
| Job | 15% | Pod, Job |
| JobSet | 35% | Pod, Job, JobSet |
| LeaderWorkerSet | 10% | Pod, StatefulSet, leader Pod, StatefulSet, LeaderWorkerSet |
| Bare Pod | 5% | the Pod is the root |

Every type except bare Pods has 4 Pods (`PODS_PER_WORKLOAD`). KWOK keeps Job Pods
Running: up.sh removes the upstream `pod-complete` stage.

## Phases

| Phase | What happens | Measured |
| --- | --- | --- |
| create | kube-burner creates the roots at `CREATE_QPS` | time to create |
| sync | wait for every load Pod, then for a row for every Pod | rows after Pods |
| steady | no change for `PHASES_STEADY` seconds | memory, scrape time, API requests |
| churn | each cycle replaces `PHASES_CHURN_PERCENT` of the roots and fails `PHASES_CHURN_FAIL_PERCENT` of Running Pods | API requests during churn |
| drain | wait until the queues are empty and the rows match | time to drain |
| recover | restart the exporter, wait for rows from the new Pod | time to recover |

Churn deletes roots with background cascade and creates copies under new names
(`-c<cycle>`), so a copy never waits for the old children to be collected. A failed
Pod gets the annotation `loadtest.karta/fail=true`, and the KWOK stage in
`kwok/stages.yaml` moves it to Failed. Its controller replaces it.

`PHASES_START=steady` or `churn` reuses the workloads of an earlier run. The run
first waits until the newest exporter Pod is scraped, has empty queues and has a
row for every load Pod, so the steady phase never includes a startup sync after
`make loadtest-up` restarts the exporter.

The sync and recover times have the resolution of the scrape interval (30s) plus
the 15s poll.

## Configuration

`loadtest.env` holds every setting and the version pins. `tiers.env` holds the
defaults for each tier and `budgets.env` the budgets. Each setting is a make
variable:

```sh
make loadtest-run TIER=50k PHASES_STEADY=1800 PHASES_CHURN_PERCENT=20
make loadtest-up TIER=custom SCALE_PODS=80000 SCALE_NODES=400 EXPORTER_API_QPS=80
```

The exporter runs with `GOMEMLIMIT` at 90% of the memory budget of the tier
(`EXPORTER_GO_MEM_LIMIT=budget`), so its GC behaves as in a Pod sized to that
budget even when `EXPORTER_MEMORY_LIMIT` leaves room. Set `EXPORTER_GO_MEM_LIMIT=auto`
for the chart default, 90% of the container limit.

## Results

Each run writes `results/<tier>-<time>/`:

- `config.env`: the settings the run used.
- `phases.env`: phase start times and the measured points.
- `report.txt` and `summary.json`: the budget table.
- `kube-burner.log` and `churn/`: the create log and the objects each churn cycle replaced.
- `pprof/`: heap, allocs and goroutine profiles after steady, drain and recover. A
  heap profile is taken after a forced GC, so it is the live heap; the report lists
  it as `exporter live heap`, which compares across runs better than heap in use.
  CPU profiles cover the last minute of steady (`steady-cpu.pb.gz`) and the first
  `PHASES_CHURN_CPU_PROFILE` seconds of churn (`churn-cpu.pb.gz`). Compare two runs
  with `go tool pprof -diff_base <old> <new>`.

`make loadtest-report` prints the table again. It exits 1 when a budget fails.

## Exporter metrics

The exporter serves Go, process, client-go and workqueue metrics on `/metrics`.
See [exportermetrics](../../dsx-workload-exporter/pkg/exportermetrics/README.md). The
report uses `workqueue_depth` and `rest_client_requests_total`.

## Limits

- kind holds the 10k and 50k tiers. 150k needs a control plane with approximately
  8 to 16 vCPU and 32 to 64 GB of memory.
- DaemonSets that tolerate every taint, such as kindnet and kube-proxy, also get
  fake Pods on each KWOK node. They are outside the load namespaces.
- kube-burner runs with a cluster-admin ServiceAccount. Use this scale test only on a
  disposable cluster.
- GPU inputs, DCGM and the PromQL joins are not part of this test.
