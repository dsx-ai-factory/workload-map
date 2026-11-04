<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Grafana dashboard examples

Two dashboards cover the main flow: find a workload, then inspect what is inside
it. They use native Grafana panels and refresh every 30 seconds. These are
editable examples for an existing Grafana and Prometheus installation.

## All workloads

[Import Workload Overview](workload-overview.json). Browse workload identities,
phase flags and age, then compare GPU, CPU and memory measurements in separate
sections. Each resource section pairs current values with history. Select a
workload name to open its structure with the same datasource and source jobs.

![Workload overview with inventory, status and resource sections](screenshots/workload-overview.png)

## Inside one workload

[Import Workload Structure](workload-structure.json). Select one workload by UID.
Follow its components, instances, groups and Pods in the graph. Nodes show maximum
measured GPU utilization and framebuffer use. Click a node for CPU, working-set
memory and identity details. Grafana opens these details on click, not hover.
The exact attribution table remains available below the graph.

![Workload structure, resource details and phase history](screenshots/workload-structure.png)

[Metric Samples](metric-samples.json) is an optional troubleshooting dashboard.
It shows all labels and exporter targets for any of the four raw DSX metrics.
It does not need recording rules.

## Set up Prometheus

1. [Install the exporter](../../../docs/dsx-workload-exporter/installation.md)
   and configure Prometheus to scrape it. Confirm `dsx_workload_pod_info` or
   `dsx_workload_status` returns samples in Prometheus before importing a dashboard.
2. Load both rules from [recording-rules.yaml](recording-rules.yaml).
   `dsx:workload:observed` identifies published workloads.
   `dsx:pod_attribution:unique` merges identical mappings and rejects conflicts.
3. Reload Prometheus through your installation's normal procedure. Check its
   Rules page: both recording rules should be loaded and evaluating without errors.

For a native Prometheus configuration, mount the file and add its path:

```yaml
rule_files:
  - /etc/prometheus/rules/dsx/recording-rules.yaml
```

For Prometheus Operator, place the file's `groups` under `spec.groups` in a
`PrometheusRule` selected by your Prometheus installation. The supplied YAML is
native rule syntax, not a Kubernetes resource. An empty query result alone cannot
distinguish missing rules from no published workloads.

[alerting-rules.yaml](alerting-rules.yaml) is optional. Load it separately to
report an observation failure that persists for five minutes. Its duration and
warning severity are example policy.

## Import into Grafana

1. Add your Prometheus server under Connections -> Data sources -> Prometheus.
   Save it and run Grafana's connection test.
2. Open Dashboards -> New -> Import. Upload `workload-overview.json`, then
   `workload-structure.json`. Import `metric-samples.json` if useful.
3. In the dashboard, select your Prometheus datasource and `exporter_job`.
   Select one or more namespaces. In Structure, select one `workload_uid`; its
   option includes the workload name so recreated workloads remain distinct.
   It includes identities seen within the selected time range, including deleted
   workloads. Current-value panels can be empty while their history remains.
4. Select `cadvisor_job` for CPU/memory and `dcgm_job` for GPU measurements.
   These sources are optional. Inventory and status work without them.
5. Choose a time range in Grafana. Hover a history interval to see its start and
   end times. Select a workload in Overview to investigate it in Structure.

Use one cluster per datasource and one exporter job at a time. That job must
contain replicas of one exporter configuration. For a backend with several
clusters, adapt the rules and joins as described in
[Several clusters in one backend](../../../docs/dsx-workload-exporter/queries.md#several-clusters-in-one-backend).

## Resource measurements

| Source | Required labels | Meaning |
| --- | --- | --- |
| cAdvisor | Workload `namespace`, `pod` and `container` | CPU cores averaged over five minutes; working-set memory in bytes |
| DCGM Exporter | Workload `namespace`, nonempty `pod_uid` and GPU device identity | Maximum measured GPU utilization; framebuffer use converted from MiB to bytes |

Enable DCGM's Pod UID mapping as described in the
[GPU query guide](../../../docs/dsx-workload-exporter/queries.md#3-gpu-measurements-by-pod-uid).
Use one external source job and one measurement stream per container or GPU
entity after source deduplication. cAdvisor joins retain their
[Pod-name reuse and rate-window limits](../../../docs/dsx-workload-exporter/queries.md#2-cpu-and-memory-by-component-instance-and-group).

GPU framebuffer use is memory reported by the matched GPU devices. Shared device
rows count once within each graph node or workload, including MIG entity identity.
Conflicting readings are excluded; missing devices make coverage partial. It is not a
measurement of memory allocated by an individual process. Low GPU utilization
does not prove a workload is idle. Missing telemetry, missing Pod UIDs and
rejected mappings remain unavailable.

## Read status and history

Phase flags are independent: a workload can match more than one phase. The
history keeps separate rows instead of inventing a priority between phases.
Colored intervals identify the matching phase. Gaps mean no accepted observation,
not a successful or stopped workload. Prometheus history starts when samples are
collected; these dashboards do not reconstruct earlier Kubernetes transitions.

A zero-Pod root remains in the inventory and graph. Published Pod mapping counts
are accepted emitted mappings, not desired replica counts or proof of complete
discovery. Inventory can include retained-invalid observations. A missing failure
row does not prove health. See the
[metric reference and validity limits](../../../docs/keps/0001-metrics-exporter/README.md#metrics).

The attribution rule preserves built-in labels and optional `team`. For other
[configured labels](../../../docs/dsx-workload-exporter/configuration.md#configured-labels),
extend both rule grouping lists and affected dashboard joins to detect conflicts
in those labels. An absent `team` label works.

## Validation

Tested with Grafana 13.2.4, Prometheus/promtool 3.14.0 and Kubernetes 1.34.0 on
Kind, including imports, panel queries, graph details, workload links and Grafana
served below a URL subpath. Overview uses isolated workloads and real cAdvisor
CPU/memory measurements. The structure screenshot uses a synthetic hierarchy,
resource values and phase transitions to show each level. All GPU readings are
synthetic fixtures; physical GPU collection was not tested.

From this directory:

```sh
python3 tests/check_dashboards.py
promtool check rules --lint-fatal recording-rules.yaml alerting-rules.yaml
promtool test rules tests/*.test.yaml
```
