<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Copyright (c) 2026 NVIDIA Corporation -->

# KEP-0001: Karta metrics exporter

- Status: provisional; backed by a prototype
- Authors: @AviadHayumi
- Issue: https://github.com/dsx-ai-factory/workload-map/issues/343
- Created: 2026-10-12
- Last updated: 2026-10-13

## Summary

The Karta metrics exporter has two main goals:

1. Show the status of selected root workloads, such as `Running`, `Failed`, `Degraded`, `Completed` or `Suspended`.
2. Show where each Pod belongs: its workload, component, named instance and repeated group. For example, a Pod in JobSet `train` can belong to component `replicatedjob`, instance `worker` and group `0`.

Observation health tells consumers whether these results can be trusted.

![The Karta exporter records metrics through its OpenTelemetry SDK and Prometheus reader. Prometheus scrapes Karta and external DCGM/cAdvisor exporters. Grafana and example decision tools consume query results.](metrics-architecture.png)

## Motivation

A workload average can hide a busy component or group. Karta makes these
relationships queryable without per-operator PromQL.

Examples:

- Dynamo: find quiet prefill GPUs while decode stays busy; check demand before changing capacity.
- LWS: find a quiet worker group hidden by the workload average.
- Workload state: see Running, Failed or Degraded workloads and their observed history.

<details>
<summary>Tools that can use these signals</summary>

- [KEDA](https://keda.sh/docs/2.18/scalers/prometheus/) can scale a compatible target from a Prometheus query.
- [Kyverno](https://kyverno.io/docs/policy-types/cluster-policy/external-data-sources/) can use a query API response as policy context.
- [Argo Rollouts](https://argoproj.github.io/argo-rollouts/features/analysis/) can evaluate Prometheus results during a rollout.

These integrations have not been validated. Sampled phase history does not provide
exact start times, restart counts or a diagnosis of flakiness.

</details>

## Proposal

### 1. Three metrics

Three gauges form the contract.

| Metric                                 | Prometheus type | Value                                           | Why it is needed                                                                 |
| -------------------------------------- | --------------- | ----------------------------------------------- | -------------------------------------------------------------------------------- |
| `karta_pod_workload_info`            | Gauge           | Always `1`                                    | Attach workload, component and group identity to measurements from a Pod.        |
| `karta_workload_status`              | Gauge           | `1` for a matching phase, otherwise `0`     | Distinguish running, completed or failed work, including workloads with no Pods. |
| `karta_exporter_observation_healthy` | Gauge           | `1` after fresh verification, otherwise `0` | Tell whether Karta's observations can be trusted. `reason` explains a failure. |

<details>
<summary>Examples: Dynamo, LWS and JobSet</summary>

Illustrative values and shortened UIDs show the metric format.

```prometheus
karta_pod_workload_info{namespace="team-a",workload="dynamo",workload_group="nvidia.com",workload_version="v1alpha1",workload_kind="DynamoGraphDeployment",workload_uid="w-d",owner_resolution="resolved",pod="prefill-0",pod_uid="p-d1",component="service",component_instance="prefill",replica_group_id=""} 1
karta_pod_workload_info{namespace="team-a",workload="dynamo",workload_group="nvidia.com",workload_version="v1alpha1",workload_kind="DynamoGraphDeployment",workload_uid="w-d",owner_resolution="resolved",pod="frontend-0",pod_uid="p-d0",component="service",component_instance="Frontend",replica_group_id=""} 1
karta_pod_workload_info{namespace="team-a",workload="dynamo",workload_group="nvidia.com",workload_version="v1alpha1",workload_kind="DynamoGraphDeployment",workload_uid="w-d",owner_resolution="resolved",pod="decode-0",pod_uid="p-d2",component="service",component_instance="decode",replica_group_id=""} 1
karta_pod_workload_info{namespace="team-a",workload="serve",workload_group="leaderworkerset.x-k8s.io",workload_version="v1",workload_kind="LeaderWorkerSet",workload_uid="w-l",owner_resolution="resolved",pod="serve-0-1",pod_uid="p-l1",component="worker",component_instance="",replica_group_id="0"} 1
karta_pod_workload_info{namespace="team-a",workload="serve",workload_group="leaderworkerset.x-k8s.io",workload_version="v1",workload_kind="LeaderWorkerSet",workload_uid="w-l",owner_resolution="resolved",pod="serve-1-1",pod_uid="p-l2",component="worker",component_instance="",replica_group_id="1"} 1
karta_pod_workload_info{namespace="team-a",workload="train",workload_group="jobset.x-k8s.io",workload_version="v1alpha2",workload_kind="JobSet",workload_uid="w-j",owner_resolution="resolved",pod="train-worker-0",pod_uid="p-j1",component="replicatedjob",component_instance="worker",replica_group_id="0"} 1
karta_workload_status{namespace="team-a",workload="dynamo",workload_group="nvidia.com",workload_version="v1alpha1",workload_kind="DynamoGraphDeployment",workload_uid="w-d",phase="Running",owner_resolution="resolved",karta="nvidia-com-dynamographdeployment-v1alpha1",karta_source="catalog"} 1
karta_exporter_observation_healthy{scope="workload",namespace="team-a",group="nvidia.com",version="v1alpha1",kind="DynamoGraphDeployment",workload="dynamo",workload_uid="w-d",reason="none"} 1
```

Other phase/health rows are omitted. Frontend, prefill and decode are instances of the
same `service` component; instance names preserve the keys in the workload definition.
LWS/Grove-backed Dynamo can supply a group key. Dropping LWS `replica_group_id` merges worker groups.

</details>

<details>
<summary>Labels, identity and metric semantics</summary>

| Metric | All exporter-defined labels |
| --- | --- |
| `karta_pod_workload_info` | `namespace`, `workload`, `workload_group`, `workload_version`, `workload_kind`, `workload_uid`, `owner_resolution`, `pod`, `pod_uid`, `component`, `component_instance`, `replica_group_id` |
| `karta_workload_status` | `namespace`, `workload`, `workload_group`, `workload_version`, `workload_kind`, `workload_uid`, `phase`, `owner_resolution`, `karta`, `karta_source` |
| `karta_exporter_observation_healthy` | `scope`, `namespace`, `group`, `version`, `kind`, `workload`, `workload_uid`, `reason` |

The reference below uses Pod info, Status and Health for these three metrics.

| Label | Metrics | Meaning | Example |
| --- | --- | --- | --- |
| `namespace` | Pod info, Status, Health | Namespace associated with the observation. | `team-a` |
| `workload` | Pod info, Status, Health | Selected root workload object name. Pod info and Status name the same root. Empty for resource-type health. | `train` |
| `workload_group` | Pod info, Status | API group of the observed workload; empty for the core API. | `jobset.x-k8s.io` |
| `workload_version` | Pod info, Status | Version in the observed workload GVK, read through the selected definition's root GVK. Neither `metadata.resourceVersion` nor the version of an intermediate owner. | `v1alpha2` |
| `workload_kind` | Pod info, Status | Kind in the observed workload GVK. | `JobSet` |
| `workload_uid` | Pod info, Status, Health | Kubernetes UID identifying this workload's lifetime. Empty for resource-type health. | `w-j` (shortened) |
| `pod` | Pod info | Attributed Pod's name. | `train-worker-0` |
| `pod_uid` | Pod info | Kubernetes UID identifying this Pod's lifetime. | `p-j1` (shortened) |
| `component` | Pod info | Component name from the selected definition; `<unknown>` if unresolved. | `replicatedjob` |
| `component_instance` | Pod info | Named entry within the component; empty when there are no named instances, or `<unknown>` if unresolved. | `worker` |
| `replica_group_id` | Pod info | Definition-selected key for a repeated group of Pods within the workload/component/instance. Empty when no group applies. It is neither a Pod index nor a replica count. | `0` |
| `phase` | Status | Evaluated workload phase; its series value is `1` when matched and `0` otherwise. | `Running` |
| `owner_resolution` | Pod info, Status | Owner-walk outcome: `resolved` after completing the chain; `ignored_owner` after stopping at an intentional boundary. Failed walks produce Health `0` with a failure `reason`, rather than an unconfirmed root row. | `Pod -> Job -> JobSet -> ignored PlatformWrapper`: JobSet has `ignored_owner`. |
| `karta` | Status | Name of the selected Karta definition. | `jobset-x-k8s-io-jobset-v1alpha2` |
| `karta_source` | Status | Origin of that definition: `catalog` for a built-in definition or `cluster` for a Karta custom resource. | `catalog` |
| `scope` | Health | `workload` verifies one workload and its dependencies; `resource_type` verifies collection of an API type before workload names may be known. | `workload` |
| `group` | Health | API group being verified. For workload scope, identifies the affected root workload, even when a dependency failed. Empty for the core API. | `jobset.x-k8s.io` |
| `version` | Health | API version being verified, using the same scope as `group`. | `v1alpha2` |
| `kind` | Health | API kind being verified, using the same scope as `group`. | `JobSet` |
| `reason` | Health | Bounded verification result: `none` after fresh success, otherwise a failure category. Detailed dependency errors belong in logs. | `none`, `permission_denied` |

Keep UIDs in history because names can be reused. Observe each workload through one
selected definition and API version; do not emit duplicate mappings for its other served
versions. A version change replaces current labeled rows while the object's UID stays the
same. Previously collected history retains its original labels.

| Example                           | `component`     | `component_instance` | `replica_group_id` |
| --------------------------------- | ----------------- | ---------------------- | -------------------- |
| Dynamo prefill, Deployment-backed | `service`       | `prefill`            | empty                |
| JobSet, first worker Job          | `replicatedjob` | `worker`             | `0`                |
| JobSet, second worker Job         | `replicatedjob` | `worker`             | `1`                |
| LWS, leader in group 0            | `leader`        | empty                  | `0`                |
| LWS, worker in group 0            | `worker`        | empty                  | `0`                |
| LWS, worker in group 1            | `worker`        | empty                  | `1`                |

- Empty instance: the definition has no named instances, as with LWS leader/worker roles.
- Empty group ID: no repeated group applies in the selected definition. For Deployment-backed Dynamo, Pods controlled by a ReplicaSet deliberately have no group key. Two prefill Pods can both have `component_instance="prefill"` and `replica_group_id=""`; separate Pods do not automatically create separate groups.
- Missing expected group key: an attribution failure; observation health must be `0`. Do not use the empty value as a group ID or infer idleness from it. Check the reason and logs.
- Unknown component: both component labels are `<unknown>`.
- Unknown instance: `component_instance="<unknown>"`.
- Unresolved workload: no attribution row. Keep the Pod visible through inventory or unmatched-usage checks.

Each snapshot has at most one attribution per Pod, to the highest Karta-described
owner reached. Status is emitted only for selected roots, including roots with no Pods.
Resolve workload ownership independently of Pod presence. A failed owner walk emits
neither Pod info nor status for that candidate. Health is `0`: `unresolved_owner` for
a missing or mismatched owner, or `permission_denied` / `api_unavailable` for read
failures. Logs identify the failed hop; do not publish an unconfirmed root.
Replace changed mappings and remove deleted Pods. Bare Pods are excluded by default.

Status phases are `Initializing`, `Running`, `Completed`, `Failed`, `Degraded`, `Suspended`,
`Suspending`, `Resuming` and `Undefined`. After successful evaluation, emit a separate series for each `phase`, with value `0` or `1`. Several phases can be `1` at once. `Undefined=1` means no phase matched; it is not an evaluation
failure. A definition without status produces no status rows. Failed status evaluation produces no
status rows and unhealthy observation. Pod attribution is independent of status availability.
Workload state is not Pod state or component health.

<details>
<summary>Example: a root selected below an ignored owner</summary>

For `Pod -> Job -> JobSet`, select JobSet with `owner_resolution="resolved"`.
The child Job has no separate status row, even if it has its own Karta definition.
If JobSet has an owner whose GVK is in `ignoredOwnerKinds`, stop before that owner
and select JobSet with `owner_resolution="ignored_owner"` instead.

A root without status does not fall back to child status. Changes to the selected
root, definition or selection reason replace current labeled rows.

Illustrative status below an ignored wrapper, with a shortened UID and other phases omitted:

```prometheus
karta_workload_status{namespace="team-a",workload="train",workload_group="jobset.x-k8s.io",workload_version="v1alpha2",workload_kind="JobSet",workload_uid="w-j",phase="Running",owner_resolution="ignored_owner",karta="jobset-x-k8s-io-jobset-v1alpha2",karta_source="catalog"} 1
```

</details>

This follows [kube-state-metrics](https://github.com/kubernetes/kube-state-metrics/blob/main/docs/design/metrics-best-practices.md):
separate identity from changing state. A failed application can have `Failed=1`
and observation health `1`: Karta read it correctly.

</details>

### 2. Queries and idleness

These queries answer three questions:

- Which workload and component does each Pod belong to?
- How much CPU or memory does each component instance and group use?
- Which groups had only low observed GPU activity over the last five minutes?

Low activity alone does not prove idleness. Before acting, require complete, fresh
telemetry and no waiting work. Expand a query below for its steps; expand again for PromQL.

<details>
<summary>The measurements supplied by DCGM and cAdvisor</summary>

```prometheus
DCGM_FI_DEV_GPU_UTIL{namespace="team-a",pod="prefill-0",UUID="GPU-a",gpu="0"} 90
DCGM_FI_DEV_GPU_UTIL{namespace="team-a",pod="decode-0",UUID="GPU-b",gpu="0"} 1
DCGM_FI_DEV_FB_USED{namespace="team-a",pod="decode-0",UUID="GPU-b",gpu="0"} 4096
container_cpu_usage_seconds_total{namespace="team-a",pod="decode-0",container="worker"} 1234
container_memory_working_set_bytes{namespace="team-a",pod="decode-0",container="worker"} 536870912
```

GPU utilization is percent; framebuffer usage is MiB (multiply by `1048576` for bytes).
CPU is cumulative seconds: `rate()` gives cores. cAdvisor working set is bytes.
Occupied memory is not activity. A CPU-only frontend legitimately has no GPU series.

</details>

<details>
<summary>Inputs and rule ownership</summary>

The following examples assume one cluster, deduplicated sources and matching
`namespace`/`pod` labels, including DCGM.

Karta supplies the three metric families and an optional ServiceMonitor. The proposed
release chart does not install Prometheus recording or alerting rules.

The queries below are documented examples. Platform teams choose the data sources,
adapt the queries and install any recording rules they want. They also own thresholds,
alerts and scaling decisions. Recorded query results are not exporter metric families.

</details>

<details>
<summary>What to do if you have multiple clusters?</summary>

When one backend combines several clusters, add a `cluster` label to Karta, DCGM and
cAdvisor metrics through the monitoring configuration. With Prometheus Operator,
configure each source's ServiceMonitor endpoint:

```yaml
relabelings:
  - action: replace
    targetLabel: cluster
    replacement: cluster-a
```

Use the same value for all sources in one cluster and a different value for each
other cluster. An OTel Collector can supply the same label through enrichment.
Preserve `cluster` in deduplication, joins and aggregations so matching workload
or Pod names from different clusters remain separate.

</details>

<details>
<summary>1. Remove duplicates when two exporters report the same Pod</summary>

- Merge identical mappings; reject conflicting identities, including UIDs.
- Record the result for reuse, or inline its expression in the following queries.

<details>
<summary>Show an optional recording rule managed by the platform</summary>

```yaml
groups:
  - name: karta-attribution
    interval: 30s
    rules:
      - record: karta:pod_attribution:unique
        expr: |
          max by (namespace, pod, pod_uid, workload, workload_group, workload_version, workload_kind,
                  workload_uid, owner_resolution, component, component_instance, replica_group_id) (
            karta_pod_workload_info
          )
          and on (namespace, pod)
          (count by (namespace, pod) (
            group by (namespace, pod, pod_uid, workload, workload_group, workload_version, workload_kind,
                      workload_uid, owner_resolution, component, component_instance, replica_group_id) (
              karta_pod_workload_info
            )
          ) == 1)
```

</details>

</details>

<details>
<summary>2. CPU and memory by component instance and group</summary>

- Convert CPU seconds into average cores over five minutes with `rate()`.
- Exclude empty container names and `POD` sandbox rows.
- Attach Karta labels to each Pod's usage. Multiplying by `1` preserves usage.
- Sum by workload, component, named instance and repeated group.

<details>
<summary>Show the CPU query</summary>

```promql
sum by (namespace, workload_group, workload_version, workload_kind, workload, workload_uid,
        owner_resolution, component, component_instance, replica_group_id) (
  rate(container_cpu_usage_seconds_total{
    namespace!="", pod!="", container!="", container!="POD"
  }[5m])
  * on (namespace, pod)
    group_left (workload_group, workload_version, workload_kind, workload, workload_uid,
                owner_resolution, component, component_instance, replica_group_id)
    karta:pod_attribution:unique{component!="<unknown>", component_instance!="<unknown>"}
)
```

</details>

For memory, replace `rate(...)` with
`container_memory_working_set_bytes{namespace!="",pod!="",container!="",container!="POD"}`.
The same join and sum return bytes.

For average GPU percent, use valid `DCGM_FI_DEV_GPU_UTIL` samples and `avg` instead
of `sum`. Averages can hide a busy device.

</details>

<details>
<summary>3. Find low GPU activity over five minutes</summary>

- Find each observed device's maximum utilization over five minutes.
- Attach Karta labels; keep the highest device value per group.
- Keep groups below `5%` whose workload is Running now.
- Exclude recorded health failures across all reasons. Filtering to `reason="none"` would hide failures.

<details>
<summary>Show the low-GPU-activity query</summary>

```promql
(
  max by (namespace, workload_group, workload_version, workload_kind, workload, workload_uid,
          owner_resolution, component, component_instance, replica_group_id) (
    max_over_time(DCGM_FI_DEV_GPU_UTIL{namespace!="",pod!=""}[5m])
    * on (namespace, pod)
      group_left (workload_group, workload_version, workload_kind, workload, workload_uid,
                  owner_resolution, component, component_instance, replica_group_id)
      karta:pod_attribution:unique{component!="<unknown>",component_instance!="<unknown>"}
  ) < 5
)
and on (namespace, workload_uid)
  (min by (namespace, workload_uid) (karta_workload_status{phase="Running"}) == 1)
and on (namespace, workload_uid)
  (min by (namespace, workload_uid) (
    min_over_time(karta_exporter_observation_healthy{scope="workload"}[5m])
  ) == 1)
```

</details>

Results keep the observed maximum. `5%` and `5m` are examples. This query cannot
detect missing devices, prove complete health history or rule out reused Pod names.

</details>

<details>
<summary>Check before stopping or scaling down a workload</summary>

Low GPU usage is a clue, not proof that a workload has no work left.
For example, a workload has two GPUs: one is busy and one is quiet. If the busy
GPU stops reporting metrics, the chart can incorrectly make the whole workload look idle.

Before acting, check:

1. Every expected Pod and GPU is reporting. Missing measurements do not mean zero usage.
2. Measurements are recent and cover the full five minutes. Karta observation health must also stay healthy throughout that time.
3. The same Pods and groups were present for the full window. After a Pod replacement, group change or health recovery, start a new five-minute window.
4. Each device is counted once. For shared or partitioned GPUs, confirm whose usage each measurement represents.
5. The application has no queued requests or jobs waiting to run. GPU metrics alone cannot tell you that.

Real GPU hardware and sharing modes have not been tested in this prototype.
See [NVIDIA's GPU-sharing limitations](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html)
when using shared GPUs.

</details>

### 3. Faults and what they affect

`karta_exporter_observation_healthy` is `1` after fresh Kubernetes reads verify the
observation; otherwise `0`. Missing health is unknown.

<details>
<summary>Examples: healthy API collection and workload recovery</summary>

Dynamo API collection has been verified in `team-a`. Workload fields are empty
because this check covers the resource type:

```prometheus
karta_exporter_observation_healthy{scope="resource_type",namespace="team-a",group="nvidia.com",version="v1alpha1",kind="DynamoGraphDeployment",workload="",workload_uid="",reason="none"} 1
```

A JobSet workload at two different times, with a shortened UID:

```prometheus
# A required Kubernetes read is denied.
karta_exporter_observation_healthy{scope="workload",namespace="team-a",group="jobset.x-k8s.io",version="v1alpha2",kind="JobSet",workload="train",workload_uid="w-j",reason="permission_denied"} 0

# After access is restored and fresh verification succeeds.
karta_exporter_observation_healthy{scope="workload",namespace="team-a",group="jobset.x-k8s.io",version="v1alpha2",kind="JobSet",workload="train",workload_uid="w-j",reason="none"} 1
```

The two workload samples are successive; recovery replaces the failure row.

</details>

| Fault | `reason` |
| --- | --- |
| Required API cannot be read | `permission_denied`, `api_unavailable` |
| Definition or evaluation fails | `invalid_definition`, `evaluation_failed` |
| Owner missing or UID mismatched below the configured boundary | `unresolved_owner` |

Collection failures also invalidate dependent workloads. Other faults affect their
workloads; an unknown owner root conservatively invalidates known workloads in that
namespace. Unrelated observations remain usable.

Recovery requires fresh verification: emit `1` with `reason="none"`, replacing the
previous reason row. Logs hold error details. Pause decisions for affected workloads
until recovery.

## Exporter configuration

Deploy the exporter separately from the Karta controller. Configure it through Helm
values; the chart generates the command-line arguments.

<details>
<summary>Helm values</summary>

```yaml
exporter:
  enabled: true # Deploy the exporter; default false.
  replicaCount: 1 # Number of exporter Pods.
  image:
    tag: "<exporter-tag>" # Exporter image version.
  namespace: team-a # Namespace to observe; empty means all namespaces.
  metricsExporter: prometheus # Expose metrics for Prometheus scraping.
  ports:
    metrics: 8080 # Metrics/probe listener, container and Service port.
  useCatalog: true # Use built-in definitions when no cluster Karta overrides them; default false.
  ignoredOwnerKinds: # Stop the owner walk before these exact GVKs; default [].
    - example.com/v1/PlatformWrapper
  requiredKinds: [] # Optional expected APIs; missing or unreadable APIs make observation unhealthy.
    # Example: ["jobset.x-k8s.io/v1alpha2/JobSet"]
  metrics:
    workloadPhases: ["all"] # Emit every workload phase as a 0/1 series.
  serviceMonitor:
    enabled: true # Create the optional ServiceMonitor; default false.
    interval: 30s # How often Prometheus scrapes metrics.
    scrapeTimeout: 25s # Maximum duration of one scrape.
```

`namespace` controls observation scope, not where Helm installs the exporter.
The ServiceMonitor requires its CRD and a matching Prometheus selector.

</details>

### Deployment settings

| Value under `exporter` | Purpose |
| --- | --- |
| `rbac.workloadRules` | Grant reads for workload kinds and intermediate owners. |
| `resources` | Set CPU and memory requests/limits for the exporter. |
| `nodeSelector`, `tolerations`, `affinity` | Choose where the exporter runs. |
| `networkPolicy` | Control access to its metrics endpoint. |

<details>
<summary>Select a root below ignored wrapper kinds</summary>

With the `ignoredOwnerKinds` value above, `Pod -> Job -> JobSet -> PlatformWrapper`
stops before reading the wrapper and attributes the Pod to JobSet. The default empty
list walks the full owner chain. If no Karta-described workload exists below the
boundary, emit no attribution. Set `owner_resolution="ignored_owner"` only when the
walk actually stops at an ignored owner; otherwise use `resolved`.

Use the same boundary for attribution, root status selection and health checks.
Missing owners below it remain failures. This is an owner-walk boundary, not a
resource exclusion: a wrapper may still have its own status if independently selected
as a root. Explicitly required API checks also remain enabled.

</details>

<details>
<summary>Example: grant reads for a JobSet-only deployment</summary>

```yaml
exporter:
  rbac:
    workloadRules:
      - apiGroups: ["jobset.x-k8s.io"]
        resources: ["jobsets"]
      - apiGroups: ["batch"]
        resources: ["jobs"]
```

The chart adds `get`, `list` and `watch`; Pod and Karta-definition reads are separate
built-in rules. Job access is needed for `Pod -> Job -> JobSet`. Overriding this list
replaces the defaults; retain other required kinds for broader deployments. A separately
managed ClusterRole labeled `karta.run.ai/exporter-rbac: "true"` can extend the aggregated role.

The chart binds a read-only aggregated ClusterRole even for an existing service account.
Namespace filtering does not narrow authorization; that requires custom RBAC manifests.

</details>

## Ways of consuming exporter metrics

Prometheus scrapes Karta's `/metrics` endpoint. Grafana and other consumers query the
collected data through Prometheus. CPU, memory and GPU measurements come from separate
exporters and can be joined with Karta's workload labels using the examples above.

| Option | Advantages | Tradeoffs |
| --- | --- | --- |
| Prometheus pull with ServiceMonitor | Declarative target discovery; direct PromQL/Grafana queries; no Collector hop. | Requires Prometheus Operator, its CRDs and matching selectors. |
| Prometheus pull with scrape configuration | Works without the Operator; same metrics and queries. | Platform maintains endpoint discovery and scrape settings. |
| Collector scrapes, then forwards OTLP | Reuses `/metrics`; central processing and routing to compatible backends. | Extra service to run; label/freshness translation needs testing; PromQL needs a compatible backend. |
| Native OTLP HTTP push | Scheduled outbound export over HTTP to an OTLP receiver. | Configure the OTel exporter; define backend handling for stale or deleted series. |
| Native OTLP gRPC push | Scheduled outbound export to an OTLP/gRPC receiver. | Same SDK configuration and freshness policy; network/proxy path must support gRPC. |

<details>
<summary>Direct Prometheus scraping: ServiceMonitor or scrape configuration</summary>

![Implemented Prometheus pull from the OTel-backed Karta exporter.](prometheus-scraping.png)

With Prometheus Operator, set `exporter.serviceMonitor.enabled: true`. The
ServiceMonitor selects the exporter Service; the Operator configures Prometheus to
scrape its endpoints. The ServiceMonitor carries configuration, not metric traffic.
See [ServiceMonitor discovery](https://prometheus-operator.dev/docs/developer/getting-started/#using-servicemonitors).

```text
ServiceMonitor -> Operator -> Prometheus scrape configuration
Prometheus -- GET /metrics --> Karta exporter
```

Without the Operator, configure the same HTTP scrape directly in Prometheus.

Prometheus discovers each exporter endpoint through the Service and scrapes it
as a distinct target. Keep target identity until query aggregation. Direct scrape
configuration must also discover endpoints; a single load-balanced ClusterIP can
hide conflicting observations during rollout. Each endpoint receives:

```http
GET /metrics HTTP/1.1
Accept: text/plain; version=0.0.4
```

The response uses the Prometheus sample format shown in the metric examples above.

For direct scraping, set `honor_labels: true` and a `30s` interval. The prototype has no endpoint TLS/authentication;
restrict access or use an authenticated proxy. A scrape does not refresh verification.

</details>

<details>
<summary>Collector and native OTLP delivery</summary>

HTTP pull uses the OTel SDK's Prometheus reader. Changing an endpoint does not
convert Prometheus exposition into OTLP; native and combined OTLP modes remain deferred.

<details>
<summary>Collector bridge: external integration, still untested</summary>

![A Collector scrapes Karta and forwards OTLP to a backend.](collector-forwarding.png)

An [OTel Collector](https://opentelemetry.io/docs/platforms/kubernetes/helm/collector/)
converts and forwards samples. It does not run the PromQL joins shown above;
those need a compatible query backend. Test identity translation, deletion and freshness.

</details>

<details>
<summary>Native OTLP: future flow and payload</summary>

![Future native OTLP delivery through a periodic OTel reader.](otlp-export.png)

On each configured interval, an [OTel reader](https://opentelemetry.io/docs/specs/otel/metrics/sdk/#periodic-exporting-metricreader)
collects and pushes current metric values, timestamps and labels, including unchanged
values. It sends no workload manifests. Prometheus triggers collection by scraping;
push uses the exporter's timer. Collections may span several requests.

HTTP uses `POST /v1/metrics`; gRPC uses `MetricsService.Export`. Both carry gauge
points like this (simplified notation):

```text
karta_workload_status: value=1, timestamp=<time>,
attributes={namespace: team-a, workload_uid: w-j, phase: Running, ...}
```

Deleted objects stop appearing. The backend needs expiry or stale-series handling;
omission is not an immediate delete command, and history remains. Delivery success
does not certify observation health. See the [OTLP data model](https://opentelemetry.io/docs/specs/otel/metrics/data-model/#gauge).

</details>
</details>

## Inside the exporter

The exporter gives Kubernetes objects to the Karta library and exposes a cached snapshot.
Operator-specific selectors stay in definitions, not exporter adapters.

<details>
<summary>How the exporter follows cluster changes</summary>

1. Load definitions. Read cluster Karta definitions and optional catalog fallbacks. Select one per workload group and kind; cluster definitions take precedence. Re-evaluate the selection when definitions change.
2. Watch workloads. List and watch the selected resource types. Resolve each workload's owner chain and publish status only for selected roots, even without Pods. Use Karta to evaluate status and component instances. Recompute affected roots when workloads, owners or definitions change; remove deleted workloads and their Pod attribution.
3. Watch Pods and owners. Follow each Pod's owner chain and use the selected Karta to resolve component, instance and repeated group. Update its single attribution when it changes; remove it on deletion. Watch intermediate owners too and retry incomplete chains.

Definition ties use oldest creation time, then name. Owner walks follow controller
references within a namespace, up to eight steps, respecting `ignoredOwnerKinds`.
Independent services and component lifecycle need separate contracts.

OTel gauges read the cached snapshot when metrics are collected. Background checks
read Kubernetes directly, compare workload state and Pod attribution, and update
observation health. Deleted objects disappear from current samples; previously
collected Prometheus history remains.

![Kubernetes API watches and background verification feed the Karta exporter snapshot, which Prometheus scrapes.](exporter-lifecycle.png)

</details>

## History

- 2026-10-12 to 2026-10-13: Proposed three metric families, replica-group identity, observation health and an HTTP-pull design backed by prototype tests. Clarified fault handling and recovery; implementation of the revised contract remains pending, and native OTLP delivery is deferred.
