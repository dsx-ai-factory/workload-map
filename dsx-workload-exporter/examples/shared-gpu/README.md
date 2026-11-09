<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Shared GPU attribution rules

Recording rules that attribute GPU measurements to the consumer Pods of a GPU that
a sharing scheduler holds through a reservation Pod. The background, the three
`labelRules` entries they need (`gpu_group`, `gpu_uuid` and `node`) and the query
guidance are in [queries.md](../../../docs/dsx-workload-exporter/queries.md),
section 3a.

## Files

- `shared-gpu-rules.yaml`: the rule group for one cluster per backend. It includes
  `dsx:pod_attribution:unique` extended with the `gpu_group`, `gpu_uuid`, `node`
  and optional `pod_phase` labels. Load it in place of the section 1 rule, not
  next to it. Add any other configured label (for example `team` from section 1)
  to both lists of that rule. A sharing group is keyed by `(gpu_group, node)`: the
  scheduler keeps one reservation Pod per node and group, and a group value can
  appear on more than one node.
- `shared-gpu-rules.cluster.yaml`: the same rules with `cluster` in every
  matching key, for backends that hold several clusters.
- `tests/`: promtool unit tests over synthetic series.

## Series

| Series | Meaning |
| --- | --- |
| `dsx:shared_gpu_device:candidate` | Every valid reservation row whose `gpu_uuid` has the `GPU-` UUID form, keyed by `UUID`, `gpu_group` and `node`. Not a join key. |
| `dsx:shared_gpu_device:group` | Candidates whose device and `(gpu_group, node)` pair map one to one. The join key. |
| `dsx:shared_gpu_device:ambiguous` | Candidates excluded by the one-to-one guard. |
| `dsx:shared_gpu_group:consumers` | Consumers observed per group and node, excluding the reservation namespace and excluding Succeeded and Failed Pods when `pod_phase` is configured. |
| `dsx:shared_gpu_util:device` | Device utilization for each mapped shared device, with its group and node. |
| `dsx:shared_gpu_util:by_consumer` | The device value repeated on every consumer. Do not sum across consumers. |
| `dsx:shared_gpu_util:equal_share` | The device value split evenly across the consumers present. |
| `dsx:shared_gpu_device:unattributed` | Mapped devices whose group has no consumer on that node. |
| `dsx:shared_gpu_group:unmapped_consumers` | Groups with consumers but no usable reservation row on their node. |
| `dsx:shared_gpu_device:unmeasured` | Mapped devices with no DCGM series. |

## Tests

```sh
promtool check rules --lint-fatal shared-gpu-rules.yaml shared-gpu-rules.cluster.yaml
cd tests && promtool test rules shared-gpu.test.yaml edge-cases.test.yaml multicluster.test.yaml
```

Each test is named for the case it covers: two consumers on one device, churn
with and without staleness markers, ambiguous device and group pairs, missing or
invalid reservation rows, unmeasured devices, duplicate DCGM scrapes, hourly
GPU-hours conservation, and two clusters that reuse a group value. Tested with
promtool 3.15.1.

## Limits

The device input is `DCGM_FI_DEV_GPU_UTIL`, from 0 to 100 percent. The default in
[dcgm-exporter 4.9.0](https://github.com/NVIDIA/dcgm-exporter/releases/tag/4.9.0)
is `DCGM_FI_DEV_GPU_UTIL_RATIO`, from 0 to 1. For that default, replace the device
input in the chosen rules file with `100 * DCGM_FI_DEV_GPU_UTIL_RATIO` before
applying the joins. These tests cover the percentage input.
Use physical-device utilization samples; per-Pod process utilization is not a
substitute.

The rules do not handle multi-device fractional Pods (one `runai-gpu-group/<id>`
label per device) or MIG. A reservation whose annotation is not a `GPU-` UUID (a
device index, a MIG id) is never mapped, and its consumers are reported as
unmapped. The `node` label must come from `spec.nodeName` on reservation and
consumer rows alike; a consumer on a different node from its reservation Pod is
reported as unmapped. The equal share is device busy time apportioned by count,
not a compute share: a GPU fraction is a granted share, not a measured one. A consumer that
is Succeeded or Failed but still present is counted unless the `pod_phase` label
is configured.
