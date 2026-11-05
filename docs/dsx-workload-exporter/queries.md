<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter queries

These examples join DSX workload identity with resource measurements. See the
[metric reference](../keps/0001-metrics-exporter/README.md#metrics) and [configuration](configuration.md) for inputs.

## Current observations and workload age

Filter Pod attribution and status by `observation_valid="true"` for verified
observations. A workload can have verified status even when its phase is Failed.

```promql
dsx_workload_status{phase="Running",observation_valid="true"} == 1
```

```promql
dsx_workload_pod_info{observation_valid="true"}
```

No health join is needed for these queries. Use
`dsx_exporter_observation_valid` for collection health and workload failure
reasons. Scrape freshness remains a separate requirement.

Workload age in seconds comes from the root's creation timestamp:

```promql
time() - max by (namespace, workload_name, workload_uid) (
  dsx_workload_created_timestamp_seconds
)
```

This metric has only `namespace`, `workload_name` and `workload_uid` as exporter
defined labels. It has no `observation_valid` label. Missing timestamps or failed
root verification omit the sample. Status evaluation failure alone does
not invalidate verified creation metadata. The aggregation merges replicas that
report the same creation time; it does not detect disagreement between replicas.

## Inputs and identity

DCGM and cAdvisor supply the resource measurements. Their Pod-name label remains
`pod`; DSX uses `pod_name`.

```prometheus
DCGM_FI_DEV_GPU_UTIL{namespace="team-a",pod="prefill-0",UUID="GPU-a",gpu="0"} 90
DCGM_FI_DEV_GPU_UTIL{namespace="team-a",pod="decode-0",UUID="GPU-b",gpu="0"} 1
DCGM_FI_DEV_FB_USED{namespace="team-a",pod="decode-0",UUID="GPU-b",gpu="0"} 4096
container_cpu_usage_seconds_total{namespace="team-a",pod="decode-0",container="worker"} 1234
container_memory_working_set_bytes{namespace="team-a",pod="decode-0",container="worker"} 536870912
```

GPU utilization is percent; framebuffer usage is MiB (multiply by `1048576` for
bytes). CPU is cumulative seconds: `rate()` gives cores. cAdvisor working set is
bytes. Occupied memory is not activity. A CPU-only frontend can have no GPU series.

On every input, `namespace` must identify the workload namespace. External `pod`
and DSX `pod_name` must identify the measured Pod, not the exporter Pod. If scrape
label collisions produce `exported_namespace` or `exported_pod`, normalize them
before using these examples. Keep exporter target labels until deduplication.

The CPU query copies the external `pod` value into `pod_name` with
[`label_replace`](https://prometheus.io/docs/prometheus/latest/querying/functions/#label_replace).
The original `pod` label stays intact. GPU examples use `pod_uid` on both sides
instead; a name-only join can match a replacement Pod to its predecessor.

The examples assume one cluster and one measurement series per container or GPU
entity after external-source deduplication. The DSX rule below only deduplicates
attribution; duplicate cAdvisor scrapes would still inflate a CPU sum. Preserve
container and device identity when deduplicating those sources.

For extra metadata, join labels or annotations exposed by
[kube-state-metrics](https://github.com/kubernetes/kube-state-metrics/blob/main/docs/developer/cli-arguments.md)
with the relevant allowlists enabled. Relabeling can copy existing sample labels
or add static labels; it does not fetch arbitrary workload metadata.

### Several clusters in one backend

Add a `cluster` label to DSX, DCGM and cAdvisor metrics through the monitoring
configuration. With Prometheus Operator, configure each source's ServiceMonitor
endpoint:

```yaml
relabelings:
  - action: replace
    targetLabel: cluster
    replacement: cluster-a
```

Use the same value for all sources in one cluster and a different value for each
other cluster. Preserve `cluster` in every deduplication, join and aggregation key
below, including workload age. An OTel Collector can supply the same label.

## 1. Deduplicate Pod attribution

Merge identical mappings and reject differences in identity, configured labels or
observation validity. Check conflicts before filtering for valid mappings so that
one valid replica cannot hide a failing replica.

Here, `team` is the custom label from the
[label rule example](configuration.md#configured-labels).
That rule reads the workload root's `metadata.labels["example.com/team"]`.
For `example.com/team: research`, the expected metric label is `team="research"`.

```yaml
groups:
  - name: dsx-attribution
    interval: 30s
    rules:
      - record: dsx:pod_attribution:unique
        expr: |
          max by (namespace, pod_name, pod_uid, workload_name, workload_group, workload_version,
                  workload_kind, workload_uid, owner_resolution, component, component_instance,
                  replica_group_id, observation_valid, team) (
            dsx_workload_pod_info
          )
          and on (namespace, pod_name)
          (count by (namespace, pod_name) (
            group by (namespace, pod_name, pod_uid, workload_name, workload_group, workload_version,
                      workload_kind, workload_uid, owner_resolution, component, component_instance,
                      replica_group_id, observation_valid, team) (
              dsx_workload_pod_info
            )
          ) == 1)
```

For other `labelRules`, add those label names to both lists above and to the
`group_left` and `sum by` lists in the following queries. Otherwise aggregation
drops them and the conflict check cannot detect disagreements in those values.

An absent `team` label also works. The rule rejects conflicting Pod UIDs for the
same name while both are visible. The following queries use this rule; its
expression can also be inlined.

## 2. CPU and memory by component instance and group

Convert CPU seconds into average cores over five minutes, exclude sandbox rows,
attach verified DSX mappings, then sum by workload and component identity.
Multiplying by an attribution value of `1` preserves the measurement.

```promql
sum by (namespace, workload_group, workload_version, workload_kind, workload_name, workload_uid,
        owner_resolution, component, component_instance, replica_group_id, team) (
  label_replace(
    rate(container_cpu_usage_seconds_total{
      namespace!="", pod!="", container!="", container!="POD"
    }[5m]),
    "pod_name", "$1", "pod", "(.+)"
  )
  * on (namespace, pod_name)
    group_left (workload_group, workload_version, workload_kind, workload_name, workload_uid,
                owner_resolution, component, component_instance, replica_group_id, team)
    dsx:pod_attribution:unique{observation_valid="true",component!="<unknown>",component_instance!="<unknown>"}
)
```

For memory, replace the `rate(...)` argument inside `label_replace` with
`container_memory_working_set_bytes{namespace!="",pod!="",container!="",container!="POD"}`.
Keep the label copy, join and sum. The result is bytes.

These name-based cAdvisor joins retain their Pod-name reuse limitation. They also
attach the current mapping to a five-minute CPU rate; they do not establish that
component attribution stayed unchanged throughout that window.

## 3. GPU measurements by Pod UID

Enable DCGM's `DCGM_EXPORTER_KUBERNETES_ENABLE_POD_UID=true` (default false), enable
Kubernetes mapping and required Pod-read access, and verify `pod_uid` in the output.
See [DCGM Exporter configuration](https://docs.nvidia.com/datacenter/dcgm/latest/reference/command-line-reference/dcgm-exporter.html).
The sample measurements above omit this optional label; the GPU queries below
require it and exclude measurements without a UID.

```promql
DCGM_FI_DEV_GPU_UTIL{namespace!="",pod_uid!=""}
* on (namespace, pod_uid)
  group_left (workload_group, workload_version, workload_kind, workload_name, workload_uid,
              owner_resolution, component, component_instance, replica_group_id, team)
  dsx:pod_attribution:unique{observation_valid="true",component!="<unknown>",component_instance!="<unknown>"}
```

The join retains external Pod and device labels. For average GPU utilization,
aggregate the joined result with `avg by` using the workload and component keys
from the CPU query. Deduplicate physical measurements first; averages can hide a
busy device. Use valid utilization samples in the range 0 to 100 and treat missing
or invalid values as missing telemetry, not zero activity.

## 3a. GPUs shared through a scheduler reservation Pod

Some GPU-sharing schedulers give a Pod part of a GPU without an `nvidia.com/gpu`
request. The scheduler creates a reservation Pod that requests `nvidia.com/gpu`; the
consumer Pods carry no such request. The kubelet
records the allocation on the reservation Pod, so DCGM's `pod`, `namespace` and
`pod_uid` labels name the reservation Pod. The query in section 3 then attributes
the device to the reservation Pod or drops it, and the consumer Pods show no GPU
measurements. Excluding reservation Pods from observation, as section 4 suggests
for idleness decisions, hides that GPU time; it does not attribute it.

KAI Scheduler's binder links the Pods of one sharing group and the reservation Pod
records the device:

- every consumer Pod and the reservation Pod carry the label `runai-gpu-group`
  with the same value; the binder keeps one reservation Pod per node and group,
  and a group value can appear on more than one node
- the reservation Pod carries the annotation `run.ai/reserve_for_gpu_index`,
  written by its container shortly after start, whose value is the GPU UUID that
  DCGM reports in `UUID`
- reservation Pods live in one namespace, `kai-resource-reservation` by default

Expose the group, the device and the node as configured labels so the exporter
emits them on `dsx_workload_pod_info`. Keep the reservation namespace inside
`namespaceSelector` and keep the catalog's `core-pod-v1` definition selected (the
catalog is enabled by default), so the reservation Pod gets its own row:

```yaml
exporter:
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
      expression: has(object.spec.nodeName) ? object.spec.nodeName : ""
```

Extend the `dsx:pod_attribution:unique` rule in section 1 with `gpu_group`,
`gpu_uuid` and `node` in both label lists. Reservation rows then carry all three
and consumer rows carry `gpu_group` and `node`. The complete rule set lives in
[`examples/shared-gpu`](../../dsx-workload-exporter/examples/shared-gpu/) with
promtool unit tests. A sharing group is keyed by `(gpu_group, node)`. The core is
one join on `UUID` followed by a fan-out on the group:

```promql
# dsx:shared_gpu_util:device
max by (UUID) (DCGM_FI_DEV_GPU_UTIL)
* on (UUID) group_left (gpu_group, node)
  dsx:shared_gpu_device:group

# dsx:shared_gpu_util:by_consumer
dsx:shared_gpu_util:device
* on (gpu_group, node) group_right (UUID)
  dsx:pod_attribution:unique{observation_valid="true",gpu_group!="",gpu_uuid=""}

# dsx:shared_gpu_util:equal_share
dsx:shared_gpu_util:by_consumer
/ on (gpu_group, node) group_left
  dsx:shared_gpu_group:consumers
```

The rules file adds `pod_phase!~"Succeeded|Failed"` to the consumer selector
above and to `dsx:shared_gpu_group:consumers`; the matcher is harmless when the
`pod_phase` label is not configured.

`dsx:shared_gpu_device:group` holds one row per shared device, taken from the
reservation rows whose annotation has the `GPU-` UUID form, and kept only while
device and group map one to one; pairs that do not are reported in
`dsx:shared_gpu_device:ambiguous` instead of failing the join for every group.
`dsx:shared_gpu_util:by_consumer` repeats the device value on every consumer, so
it answers which workloads were active on a device. Do not sum it across
consumers. `dsx:shared_gpu_util:equal_share` splits the device value evenly across
the consumers present; its selector must stay identical to the one in
`dsx:shared_gpu_group:consumers`, or device time leaks. A GPU fraction describes
the share the scheduler granted, not the compute each consumer used, so prefer
per-process measurements as weights when the sharing stack provides them.

Three series report coverage and belong on any dashboard that uses these rules:
`dsx:shared_gpu_device:unattributed` (a mapped device whose group has no
consumer), `dsx:shared_gpu_group:unmapped_consumers` (consumers whose reservation
row is missing, not yet annotated, invalid or ambiguous, usually a selection or
definition problem) and `dsx:shared_gpu_device:unmeasured` (a mapped device with
no DCGM series).

Utilized GPU-hours per workload over an hour, from the equal share:

```promql
sum by (namespace, workload_name, workload_uid) (
  sum_over_time(dsx:shared_gpu_util:equal_share[1h])
) / 12000
```

The divisor is 100 (percent) times 120 (30-second evaluations per hour); adjust
it with the group interval. This is device busy time apportioned equally, not
allocated time.

While these rules are in use, add `gpu_uuid=""` to the selectors in sections 3
and 4. That keeps a reservation Pod from being attributed as a tenant workload.

A consumer Pod that has terminated but still exists keeps its group label, so it
would be paid a share. An optional fourth label rule excludes Succeeded and
Failed Pods: `name: pod_phase`, `metrics: [dsx_workload_pod_info]`,
`sources: [pod]`, and the expression
`has(object.status) && has(object.status.phase) ? object.status.phase : ""`.

For several clusters in one backend, use the cluster-aware variant in the same
directory. It carries `cluster` in every matching key.

Two shapes are out of scope here. Multi-device fractional Pods carry one
`runai-gpu-group/<id>` label per device, and MIG devices have their own identity.

A sharing stack that exposes its own Pod-to-device series, keyed by Pod UID and
GPU UUID, can replace the reservation Pod step: join it on `UUID`, then on
`pod_uid`.

## 4. Low observed GPU activity over five minutes

Find each observed device's maximum utilization, attach its current verified
mapping and retain the highest device value per group. Keep groups below `5%`
whose workload has verified Running status now. Reject current invalid status
replicas and workload failures observed in the window.

Reservation Pods may hold GPUs at low activity by design. Exclude them from tenant
idleness decisions through the [selection settings](configuration.md#selection),
or attribute their devices to the consumers with section 3a and add `gpu_uuid=""`
to the selector below. Filtering Pods also limits how much of the workload the
query observes.

```promql
(
  max by (namespace, workload_group, workload_version, workload_kind, workload_name, workload_uid,
          owner_resolution, component, component_instance, replica_group_id, team) (
    max_over_time(DCGM_FI_DEV_GPU_UTIL{namespace!="",pod_uid!=""}[5m])
    * on (namespace, pod_uid)
      group_left (workload_group, workload_version, workload_kind, workload_name, workload_uid,
                  owner_resolution, component, component_instance, replica_group_id, team)
      dsx:pod_attribution:unique{observation_valid="true",component!="<unknown>",component_instance!="<unknown>"}
  ) < 5
)
and on (namespace, workload_uid)
  (min by (namespace, workload_uid) (
    dsx_workload_status{phase="Running",observation_valid="true"}
  ) == 1)
unless on (namespace, workload_uid)
  dsx_workload_status{observation_valid="false"}
unless on (namespace, workload_uid)
  (min_over_time(dsx_exporter_observation_valid{scope="workload"}[5m]) == 0)
```

Results keep the observed maximum. `5%` and `5m` are examples. The UID join prevents
crossing Pod lifetimes, but does not establish continuous attribution, unchanged
groups or telemetry coverage. Failure diagnostics are sparse: their absence does
not prove complete health history. Missing devices or rejected mappings can still
make a partially observed group appear quiet.

## Before stopping or scaling down a workload

If a workload has a busy GPU and a quiet GPU, losing the busy GPU's measurements
can make the whole workload appear idle. Before acting, check:

1. Every expected Pod and GPU is reporting. Missing measurements do not mean zero usage.
2. Measurements and DSX samples are recent and cover the full window. Require verified mappings and status throughout it; absence of workload failure rows alone does not establish coverage.
3. The same Pods and groups were present for the full window. After a Pod replacement, attribution change or verification recovery, start a new window.
4. Each device is counted once. For shared or partitioned GPUs, confirm whose usage each measurement represents.
5. The application has no queued requests or jobs waiting to run. GPU metrics alone cannot tell you that.

MIG may require a supported profiling metric such as
`DCGM_FI_PROF_GR_ENGINE_ACTIVE` (0 to 1), with a correspondingly adjusted threshold.
DRA may need a claim-to-Pod mapping. The query above does not establish idleness
for every GPU-sharing mode.
