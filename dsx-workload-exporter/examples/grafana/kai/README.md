<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# KAI Scheduler shared GPU attribution

When KAI Scheduler shares a single GPU across fractional Pods, consumer Pods
request no `nvidia.com/gpu` from Kubernetes. KAI allocates the physical GPU
through a reservation Pod instead. These Prometheus recording rules connect DCGM
device utilization on the reservation Pod back to the consumer Pods and their
Karta workloads without changing exporter code.

## Example: why the Pod UID join misses shared consumers

Suppose two fractional Pods (`runner-0` and `runner-1`, owned by Deployment
`runner-pool` in namespace `team-b`) share GPU `GPU-aaaa` on `node-1`, and the
device is `40%` busy.

KAI creates one reservation Pod (`gpu-reservation-1a2b3c4d` in
`kai-resource-reservation`) on `node-1` with `nvidia.com/gpu: 1`. The consumer
Pods carry no `nvidia.com/gpu` request. Because kubelet `pod-resources` only
records the device-plugin allocation on the reservation Pod, DCGM emits the
device reading under the reservation Pod's identity:

```prometheus
DCGM_FI_DEV_GPU_UTIL{UUID="GPU-aaaa",namespace="kai-resource-reservation",pod="gpu-reservation-1a2b3c4d",pod_uid="res-1"} 40
```

The standard GPU query in
[queries.md](../../../../docs/dsx-workload-exporter/queries.md#3-gpu-measurements-by-pod-uid)
and the direct-query dashboards in [`../workload-overview.json`](../workload-overview.json)
and [`../workload-structure.json`](../workload-structure.json) join
`DCGM_FI_DEV_GPU_UTIL` to `dsx_workload_pod_info` on `pod_uid`. On a shared KAI
device, that join matches `pod_uid="res-1"` (the bare reservation Pod), never
`runner-0` or `runner-1`. Excluding `kai-resource-reservation` hides that `40%`
activity instead of attributing it to `runner-pool`. Workload inventory,
structure, CPU and RAM panels remain unaffected.

KAI stamps the metadata needed to bridge the reservation Pod to its consumers:

- The binder writes the same `runai-gpu-group` label (`g-1`) onto the
  reservation Pod and onto every consumer Pod in that sharing group on `node-1`.
  One reservation Pod exists per `(gpu_group, node)` pair, and a group value can
  be reused on another node.
- Shortly after start, the reservation Pod writes the physical GPU UUID
  (`GPU-aaaa`) into its `run.ai/reserve_for_gpu_index` annotation.

Exporting `gpu_group`, `gpu_uuid` and `node` on `dsx_workload_pod_info` produces:

```prometheus
dsx_workload_pod_info{namespace="kai-resource-reservation",pod_name="gpu-reservation-1a2b3c4d",pod_uid="res-1",workload_kind="Pod",observation_valid="true",gpu_group="g-1",node="node-1",gpu_uuid="GPU-aaaa"} 1
dsx_workload_pod_info{namespace="team-b",pod_name="runner-0",pod_uid="r-0",workload_name="runner-pool",workload_kind="Deployment",observation_valid="true",gpu_group="g-1",node="node-1"} 1
dsx_workload_pod_info{namespace="team-b",pod_name="runner-1",pod_uid="r-1",workload_name="runner-pool",workload_kind="Deployment",observation_valid="true",gpu_group="g-1",node="node-1"} 1
```

The rules join `DCGM_FI_DEV_GPU_UTIL` (`40`) to the reservation row on
`UUID="GPU-aaaa"` to obtain `(gpu_group="g-1", node="node-1")`, then fan out to
the active consumer Pods in that group on `node-1`:

- `dsx:shared_gpu_util:by_consumer` reports `40` on `runner-0` and `40` on
  `runner-1`.
- `dsx:shared_gpu_util:equal_share` splits `40` across the `2` active consumers,
  reporting `20` on `runner-0` and `20` on `runner-1`.

## Exporter configuration

Follow [../setup.md](../setup.md) for Prometheus and DCGM Exporter setup, and
add these three Pod label rules to the Karta Helm values under
`exporter.metrics.labelRules`:

```yaml
exporter:
  enabled: true
  metrics:
    labelRules:
      - name: gpu_group
        metrics: [dsx_workload_pod_info]
        sources: [pod]
        expression: |
          has(object.metadata.labels) && "runai-gpu-group" in object.metadata.labels
            ? object.metadata.labels["runai-gpu-group"] : ""
      - name: gpu_uuid
        metrics: [dsx_workload_pod_info]
        sources: [pod]
        expression: |
          has(object.metadata.annotations) && "run.ai/reserve_for_gpu_index" in object.metadata.annotations
            ? object.metadata.annotations["run.ai/reserve_for_gpu_index"] : ""
      - name: node
        metrics: [dsx_workload_pod_info]
        sources: [pod]
        expression: |
          has(object.spec.nodeName) ? object.spec.nodeName : ""
```

Keep the reservation namespace (`kai-resource-reservation` by default) included
in `exporter.selection.namespaceSelector` and keep the bundled `core-pod-v1`
definition enabled under `exporter.kartaDefinitionSource.catalog`. Both are
enabled by default in [`charts/karta/values.yaml`](../../../../charts/karta/values.yaml),
giving each reservation Pod its own `dsx_workload_pod_info` row.

If terminated consumer Pods remain on the node, add an optional fourth rule so
`Succeeded` and `Failed` Pods stop receiving shares:

```yaml
      - name: pod_phase
        metrics: [dsx_workload_pod_info]
        sources: [pod]
        expression: |
          has(object.status) && has(object.status.phase) ? object.status.phase : ""
```

When using the standalone binary (`--config`), place `metrics.labelRules` at the
YAML root without the `exporter:` wrapper. If KAI uses a non-default reservation
namespace, replace `namespace!="kai-resource-reservation"` in both consumer
selectors of the chosen rules file.

## Shared device usage is not measured usage per consumer

`DCGM_FI_DEV_GPU_UTIL` measures the physical GPU as a whole, not individual
containers or processes sharing it.

`dsx:shared_gpu_util:by_consumer` repeats the whole-device value on every active
consumer of that GPU (`40` on both `runner-0` and `runner-1` in the example).
Use it to check whether a consumer's underlying GPU is active or quiet, where a
low value means the entire shared device was quiet. Do not sum `by_consumer`
across Pods, or one physical GPU is counted once per consumer.

`dsx:shared_gpu_util:equal_share` divides the physical GPU value evenly across
the active consumers present in the group (`20` on each consumer in the
example). Summing `equal_share` across consumers conserves total device busy
time, which makes it suitable for apportioning shared GPU-hours across
workloads. Neither series measures how much compute each consumer caused. If
`runner-0` drives `40%` activity while `runner-1` sits idle on the same GPU,
both still report `40` in `by_consumer` and `20` in `equal_share`. A requested
GPU fraction is a scheduler grant or limit, not a per-consumer hardware counter.

## Files and recorded series

- [`shared-gpu-rules.yaml`](shared-gpu-rules.yaml): single-cluster rule group.
  It extends `dsx:pod_attribution:unique` from
  [`../recording-rules.yaml`](../recording-rules.yaml) with `gpu_group`,
  `gpu_uuid`, `node` and optional `pod_phase` while preserving `job` and `team`.
  Load it in place of the `dsx:pod_attribution:unique` rule in
  `../recording-rules.yaml` (keeping `dsx:workload:observed` if needed), not
  next to it. Add any other configured Pod labels to both grouping lists of that
  rule.
- [`shared-gpu-rules.cluster.yaml`](shared-gpu-rules.cluster.yaml):
  multi-cluster variant with `cluster` in every matching key.
- [`tests/`](tests/): `promtool` unit tests over synthetic series.

See [queries.md](../../../../docs/dsx-workload-exporter/queries.md#3a-gpus-shared-through-a-scheduler-reservation-pod)
for the hourly GPU-hours expression, coexistence with dedicated-GPU Pod UID
queries (`gpu_uuid=""` plus reservation-namespace exclusion) and low-activity
checks.

| Series | Meaning |
| --- | --- |
| `dsx:shared_gpu_device:candidate` | Valid reservation rows whose `gpu_uuid` starts with `GPU-`, keyed by `UUID`, `gpu_group` and `node`. Not a join key. |
| `dsx:shared_gpu_device:group` | Candidates whose `UUID` and `(gpu_group, node)` pair map one to one. Used as the join key. |
| `dsx:shared_gpu_device:ambiguous` | Candidates excluded by the one-to-one guard during reservation swaps or conflicts. |
| `dsx:shared_gpu_group:consumers` | Active consumers per `(gpu_group, node)`, excluding the reservation namespace and excluding `Succeeded` and `Failed` Pods when `pod_phase` is configured. |
| `dsx:shared_gpu_util:device` | Utilization of each mapped shared device, labeled with `gpu_group` and `node`. |
| `dsx:shared_gpu_util:by_consumer` | Physical device utilization repeated on each active consumer. Do not sum across consumers. |
| `dsx:shared_gpu_util:equal_share` | Physical device utilization divided equally across active consumers in the group. |
| `dsx:shared_gpu_device:unattributed` | Mapped shared devices with no active consumer on that node. |
| `dsx:shared_gpu_group:unmapped_consumers` | Consumer groups with no usable reservation row on that node. |
| `dsx:shared_gpu_device:unmeasured` | Mapped shared devices with no current DCGM sample. |

## Scope limits and validation

The rules expect `DCGM_FI_DEV_GPU_UTIL` in percent (`0` to `100`).
[DCGM Exporter 4.9.0](https://github.com/NVIDIA/dcgm-exporter/releases/tag/4.9.0)
defaults to `DCGM_FI_DEV_GPU_UTIL_RATIO` (`0` to `1`). For that metric, replace
`DCGM_FI_DEV_GPU_UTIL` in the chosen rules file with
`100 * DCGM_FI_DEV_GPU_UTIL_RATIO`. Use physical-device samples rather than
derived virtual-GPU process metrics.

Multi-device fractional Pods (`runai-gpu-group/<id>` per device) and MIG
instances are out of scope. Reservation annotations that do not start with
`GPU-` (such as device indices or MIG identifiers) stay unmapped and surface in
`dsx:shared_gpu_group:unmapped_consumers`. Both reservation and consumer rows
must derive `node` from `spec.nodeName`.

Validate all example rule and test files from the repository root:

```sh
make promtool-check
```

Or run an installed `promtool` directly:

```sh
promtool check rules --lint-fatal \
  dsx-workload-exporter/examples/grafana/kai/shared-gpu-rules.yaml \
  dsx-workload-exporter/examples/grafana/kai/shared-gpu-rules.cluster.yaml
promtool test rules \
  dsx-workload-exporter/examples/grafana/kai/tests/shared-gpu.test.yaml \
  dsx-workload-exporter/examples/grafana/kai/tests/edge-cases.test.yaml \
  dsx-workload-exporter/examples/grafana/kai/tests/multicluster.test.yaml
```
