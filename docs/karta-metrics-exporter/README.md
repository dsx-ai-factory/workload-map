<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta metrics exporter queries

These examples use the metric contract in the
[Karta metrics exporter proposal](../../keps/0001-metrics-exporter/README.md).
They show Pod attribution, workload age, component resource usage and low observed
GPU activity. Low activity alone does not prove idleness.

## Current observations and workload age

Filter Pod attribution and status by `observation_valid="true"` for verified
observations. A workload can have verified status even when its phase is Failed.

```promql
karta_workload_status{phase="Running",observation_valid="true"} == 1
```

```promql
karta_pod_workload_info{observation_valid="true"}
```

No health join is needed for these queries. Use
`karta_exporter_observation_valid` for collection health and workload failure
reasons. Scrape freshness remains a separate requirement.

Workload age in seconds comes from the root's creation timestamp:

```promql
time() - max by (namespace, workload_name, workload_uid) (
  karta_workload_created_timestamp_seconds
)
```

This metric has only `namespace`, `workload_name` and `workload_uid` as exporter
defined labels. It has no `observation_valid` label. Missing timestamps or failed
or expired root verification omit the sample. Status evaluation failure alone does
not invalidate verified creation metadata. The aggregation merges replicas that
report the same creation time; it does not detect disagreement between replicas.

## Inputs and identity

DCGM and cAdvisor supply the resource measurements. Their Pod-name label remains
`pod`; Karta uses `pod_name`.

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
and Karta `pod_name` must identify the measured Pod, not the exporter Pod. If scrape
label collisions produce `exported_namespace` or `exported_pod`, normalize them
before using these examples. Keep exporter target labels until deduplication.

The CPU query copies the external `pod` value into `pod_name` with
[`label_replace`](https://prometheus.io/docs/prometheus/latest/querying/functions/#label_replace).
The original `pod` label stays intact. GPU examples use `pod_uid` on both sides
instead; a name-only join can match a replacement Pod to its predecessor.

The examples assume one cluster and one measurement series per container or GPU
entity after external-source deduplication. The Karta rule below only deduplicates
attribution; duplicate cAdvisor scrapes would still inflate a CPU sum. Preserve
container and device identity when deduplicating those sources.

For extra metadata, join labels or annotations exposed by
[kube-state-metrics](https://github.com/kubernetes/kube-state-metrics/blob/main/docs/developer/cli-arguments.md)
with the relevant allowlists enabled. Relabeling can copy existing sample labels
or add static labels; it does not fetch arbitrary workload metadata.

### Several clusters in one backend

Add a `cluster` label to Karta, DCGM and cAdvisor metrics through the monitoring
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

Merge identical mappings and reject differences in identity, submitter or
observation validity. Check conflicts before filtering for valid mappings so that
one valid replica cannot hide a failing replica.

```yaml
groups:
  - name: karta-attribution
    interval: 30s
    rules:
      - record: karta:pod_attribution:unique
        expr: |
          max by (namespace, pod_name, pod_uid, workload_name, workload_group, workload_version,
                  workload_kind, workload_uid, owner_resolution, component, component_instance,
                  replica_group_id, observation_valid, submitted_by) (
            karta_pod_workload_info
          )
          and on (namespace, pod_name)
          (count by (namespace, pod_name) (
            group by (namespace, pod_name, pod_uid, workload_name, workload_group, workload_version,
                      workload_kind, workload_uid, owner_resolution, component, component_instance,
                      replica_group_id, observation_valid, submitted_by) (
              karta_pod_workload_info
            )
          ) == 1)
```

This rule preserves all attribution labels, including optional `submitted_by`.
It rejects conflicting Pod UIDs for the same name while both are visible. An
absent optional label works when submitter attribution is disabled. The following
queries use this rule; its expression can also be inlined.

## 2. CPU and memory by component instance and group

Convert CPU seconds into average cores over five minutes, exclude sandbox rows,
attach verified Karta mappings, then sum by workload and component identity.
Multiplying by an attribution value of `1` preserves the measurement.

```promql
sum by (namespace, workload_group, workload_version, workload_kind, workload_name, workload_uid,
        owner_resolution, component, component_instance, replica_group_id, submitted_by) (
  label_replace(
    rate(container_cpu_usage_seconds_total{
      namespace!="", pod!="", container!="", container!="POD"
    }[5m]),
    "pod_name", "$1", "pod", "(.+)"
  )
  * on (namespace, pod_name)
    group_left (workload_group, workload_version, workload_kind, workload_name, workload_uid,
                owner_resolution, component, component_instance, replica_group_id, submitted_by)
    karta:pod_attribution:unique{observation_valid="true",component!="<unknown>",component_instance!="<unknown>"}
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
              owner_resolution, component, component_instance, replica_group_id, submitted_by)
  karta:pod_attribution:unique{observation_valid="true",component!="<unknown>",component_instance!="<unknown>"}
```

The join retains external Pod and device labels. For average GPU utilization,
aggregate the joined result with `avg by` using the workload and component keys
from the CPU query. Deduplicate physical measurements first; averages can hide a
busy device. Use valid utilization samples in the range 0 to 100 and treat missing
or invalid values as missing telemetry, not zero activity.

## 4. Low observed GPU activity over five minutes

Find each observed device's maximum utilization, attach its current verified
mapping and retain the highest device value per group. Keep groups below `5%`
whose workload has verified Running status now. Reject current invalid status
replicas and workload failures observed in the window.

Reservation Pods may hold GPUs at low activity by design. Exclude them from tenant
idleness decisions through the [selection settings](configuration.md#selection).
Filtering Pods also limits how much of the workload the query observes.

```promql
(
  max by (namespace, workload_group, workload_version, workload_kind, workload_name, workload_uid,
          owner_resolution, component, component_instance, replica_group_id, submitted_by) (
    max_over_time(DCGM_FI_DEV_GPU_UTIL{namespace!="",pod_uid!=""}[5m])
    * on (namespace, pod_uid)
      group_left (workload_group, workload_version, workload_kind, workload_name, workload_uid,
                  owner_resolution, component, component_instance, replica_group_id, submitted_by)
      karta:pod_attribution:unique{observation_valid="true",component!="<unknown>",component_instance!="<unknown>"}
  ) < 5
)
and on (namespace, workload_uid)
  (min by (namespace, workload_uid) (
    karta_workload_status{phase="Running",observation_valid="true"}
  ) == 1)
unless on (namespace, workload_uid)
  karta_workload_status{observation_valid="false"}
unless on (namespace, workload_uid)
  (min_over_time(karta_exporter_observation_valid{scope="workload"}[5m]) == 0)
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
2. Measurements and Karta samples are recent and cover the full window. Require verified mappings and status throughout it; absence of workload failure rows alone does not establish coverage.
3. The same Pods and groups were present for the full window. After a Pod replacement, attribution change or verification recovery, start a new window.
4. Each device is counted once. For shared or partitioned GPUs, confirm whose usage each measurement represents.
5. The application has no queued requests or jobs waiting to run. GPU metrics alone cannot tell you that.

MIG may require a supported profiling metric such as
`DCGM_FI_PROF_GR_ENGINE_ACTIVE` (0 to 1), with a correspondingly adjusted threshold.
DRA may need a claim-to-Pod mapping. The query above does not establish idleness
for every GPU-sharing mode.
