<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Grafana examples

Import these dashboards into an existing Grafana installation backed by
Prometheus. They are editable examples, not support for deploying or operating
Grafana. A Deployment with zero Pods still appears in the workload inventory and
structure graph. Missing CPU or GPU measurements remain unavailable.

| Dashboard | Purpose | Preview |
| --- | --- | --- |
| [Workload Overview](workload-overview.json) | Find workloads to investigate using inventory, independent phases, age and collection diagnostics. Includes optional resource measurements. | [Screenshot](screenshots/workload-overview.png) |
| [Workload Structure](workload-structure.json) | Inspect one workload's Pod attribution as a graph and an exact label table. | [Screenshot](screenshots/workload-structure.png) |
| [Metric Samples](metric-samples.json) | Inspect any of the four raw exporter metrics, preserving every label and exporter target. | [Screenshot](screenshots/metric-samples.png) |

The dashboards use built-in Grafana panels and refresh every 30 seconds. See the
[metric reference](../../../docs/keps/0001-metrics-exporter/README.md#metrics),
[exporter configuration](../../../docs/dsx-workload-exporter/configuration.md)
and [query guide](../../../docs/dsx-workload-exporter/queries.md) for their inputs
and semantics.

## Load the recording rules

Overview and Structure require both rules in
[recording-rules.yaml](recording-rules.yaml):

- `dsx:workload:observed` identifies currently published workloads.
- `dsx:pod_attribution:unique` merges identical Pod mappings and rejects conflicts.

Copy or mount this file where Prometheus can read it. Add its path to the existing
Prometheus configuration:

```yaml
rule_files:
  - /etc/prometheus/rules/dsx/recording-rules.yaml
```

Reload Prometheus using your installation's normal procedure. Confirm both rules
are loaded and evaluating before using Overview or Structure. An empty query
result alone cannot distinguish missing rules from no published workloads.
Metric Samples has no recording-rule dependency.

[alerting-rules.yaml](alerting-rules.yaml) is optional. Add its path to `rule_files`
separately to enable `DSXExporterObservationFailure`. It fires when a reported
observation failure persists for five minutes. Its duration and warning severity
are example policy. These files contain native Prometheus rule groups, not
Kubernetes `PrometheusRule` resources.

## Import and select data

1. Add a Prometheus datasource in Grafana that contains the DSX exporter scrapes.
2. Open Grafana's dashboard import page and upload one of the JSON files above.
   Repeat for the other dashboards.
3. Select the datasource in the dashboard's `datasource` picker, then choose
   `exporter_job`. Overview and Structure support multiple `namespace` values.
   In Structure, select one `workload_uid`; its option shows the workload name
   and UID. In Metric Samples, choose a metric with `metric`.

Use one cluster per datasource and one DSX exporter job at a time. The selected
job must contain replicas of one exporter configuration. For a backend containing
several clusters, adapt every input, rule and join as described in
[Several clusters in one backend](../../../docs/dsx-workload-exporter/queries.md#several-clusters-in-one-backend).

These JSON files use current DSX metrics and canonical `workload_name` and
`pod_name` labels. Import them as separate dashboards when replacing older local
panels that relied on custom observer or simulator metrics.

## Optional resource measurements

Select the external jobs independently through `cadvisor_job` and `dcgm_job` in
Overview. The inventory, structure and raw exporter views do not require them.

| Source | Required input | Display |
| --- | --- | --- |
| cAdvisor | CPU counters and working-set gauges with workload `namespace`, `pod` and `container` labels | Average CPU cores over five minutes; working-set bytes |
| DCGM Exporter | GPU utilization and framebuffer gauges with workload `namespace`, nonempty `pod_uid` and device identity | Maximum measured GPU utilization in percent; framebuffer bytes converted from MiB |

Use one external source job and one measurement stream per container or GPU
entity after source deduplication. cAdvisor joins use Pod names and retain the
[name-reuse and rate-window limits](../../../docs/dsx-workload-exporter/queries.md#2-cpu-and-memory-by-component-instance-and-group).
GPU joins require Pod UIDs; see the
[DCGM configuration requirement](../../../docs/dsx-workload-exporter/queries.md#3-gpu-measurements-by-pod-uid).
Missing measurements, missing UIDs and rejected mappings remain unavailable.
They do not mean zero usage, no assigned GPUs or an idle workload.

The supplied attribution rule supports the built-in labels and optional `team`
label from the [configured-label example](../../../docs/dsx-workload-exporter/configuration.md#configured-labels).
An absent `team` label works. For additional configured labels, extend both
attribution grouping lists and the affected dashboard groupings and joins so
conflicts in those labels remain detectable.

## Read the results

Inventory counts published workload identities, including retained invalid
observations. Phase counts are independent and must not be summed into a total.
The graph shows attribution, not Kubernetes owner references or desired replicas.
"Published valid Pod mappings" counts accepted emitted mappings; it does not
establish how many Pods should exist or whether discovery is complete.

Scrape status and sample age are separate from Kubernetes reevaluation. A target
that disappears from discovery cannot be detected without an expected-target
inventory. See [validity limits](../../../docs/keps/0001-metrics-exporter/README.md#validity-limits)
before using these views to make workload decisions.

## Validation

Tested with fresh Grafana 13.2.4, Prometheus/promtool 3.14.0 and Kubernetes 1.34.0
on Kind. All panel queries, metric selections and workload links were checked,
including Grafana served below a URL subpath. Tests cover zero-Pod roots, a
standalone Pod, a completed Job and a custom workload without a definition.

The screenshots use those test workloads. CPU and memory are real cAdvisor
measurements. GPU values are synthetic fixtures that verify joins and units,
not physical GPU collection.

From this directory, validate the examples with:

```sh
python3 tests/check_dashboards.py
promtool check rules --lint-fatal recording-rules.yaml alerting-rules.yaml
promtool test rules tests/rules.test.yaml
```
