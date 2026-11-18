<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter configuration

## Helm values

The exporter is disabled by default. These Helm values enable it and select
Deployment roots in the `workloads` namespace.

```yaml
exporter:
  enabled: true
  selection:
    namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: workloads
    includeRootResources:
      - apiGroups: ["apps"]
        apiVersions: ["v1"]
        resources: ["deployments"]
```

A Pod owned by the `trainer` Deployment through its ReplicaSet is attributed
to `trainer` in `workloads`. A Job in the same namespace and a Deployment in
`staging` fall outside this selection. The exporter runs independently of the
Karta operator. See the [installation guide](installation.md) for the Helm
command and [values.yaml](../../charts/karta/values.yaml) for chart defaults.

[Selection](#selection) | [Metrics](#metric-output) | [Scraping](#metric-delivery) |
[Deployment and access](#deployment-and-permissions) | [Tuning](#advanced-tuning-and-profiling) |
[Standalone binary](#standalone-binary-and-precedence)

## Selection

| Under `exporter.selection` | Default | Effect |
| --- | --- | --- |
| `namespaceSelector` | Excludes `kube-system`, `kube-public`, `kube-node-lease` | Matches Namespace labels. Set `matchExpressions: []` to clear the exclusions. |
| `includeRootResources` | `[]` (all types) | Lists which API types can be workload roots. |
| `excludeRootResources` | Core ConfigMaps and Secrets, all versions | Excludes root types. Set `[]` to clear the exclusions. |
| `rootSelectors` | `[]` | Matches root labels for all types or for one API version and kind. |
| `podSelector` | `{}` | Matches Pod labels for Pod attribution rows. |
| `ignoredOwnerKinds` | `[]` | Stops before an owner of one of these types. |
| `requiredWatchKinds` | `[]` | Requires watches for these types. Readiness waits for them to synchronize. |

A Pod attribution row requires both the root and the Pod to match. Workload
status covers the full workload. Excluding a root does not promote its children
to roots. Namespace selection applies to namespaced observation. RBAC controls
authorization.

Resource rules match group, version and plural resource. Entries are alternatives;
all three fields must match within an entry. Each field needs a nonempty list.
`"*"` is a wildcard and `""` is the core API group. Exclusions win. Selectors,
malformed rules and unknown catalog names are validated before observation.

### Definition sources and owner boundaries

| Under `exporter.kartaDefinitionSource` | Default | Effect |
| --- | --- | --- |
| `catalog.enabled` | `true` | Loads bundled definitions. |
| `cluster.enabled` | `true` | Watches cluster Karta definitions. |
| `catalog.include`, `catalog.exclude`, `cluster.include`, `cluster.exclude` | `[]` | Filters definitions by name within each source. |
| `enableExperimental` | `false` | Allows definitions with `spec.experimental: true` from either source. |

Source filters apply before precedence. Empty includes select all definition
names; exclusions win. A selected cluster Karta overrides the catalog for the
same group/kind. Cluster ties use oldest creation time, then name. Catalog ties
use Kubernetes version priority, then name. A source or selected-definition
error is not absence and does not trigger fallback.

Experimental definitions are excluded from both sources by default. To allow
them, add this to the Helm values file:

```yaml
exporter:
  kartaDefinitionSource:
    enableExperimental: true
```

An explicit `include` does not bypass this setting. The gate applies before version
and source selection. An excluded experimental cluster Karta can therefore reveal
a stable cluster or catalog definition. Exclusion is not a source failure.
Changing a cluster Karta's `spec.experimental` flag refreshes selection and
requeues affected workloads. Definitions that omit the flag are stable.

Cluster watching requires the Karta CRD, even when no Karta objects exist. Bundled definitions provide built-in workload support without those objects.

`ignoredOwnerKinds` contains exact GVK strings, such as
`example.com/v1/PlatformWrapper`. Traversal stops before that owner. The last
verified object below it becomes the root. A definition on an intermediate owner
does not stop traversal. Missing owners below the boundary still fail.
`requiredWatchKinds` uses the same format and adds watched kinds, including types
that have no selected definition or Pods yet.

### Root label filters

Use one `rootSelectors` list for global and kind-specific filters. An entry
without `apiVersion` and `kind` applies to every root. Set both fields to limit
an entry to one exact API version and kind:

```yaml
exporter:
  selection:
    rootSelectors:
      - selector:
          matchLabels:
            environment: production
      - apiVersion: scheduling.example.org/v1
        kind: Workload
        selector:
          matchLabels:
            app.kubernetes.io/managed-by: example-platform
```

A Deployment must have `environment=production`. A Workload of the specified
kind must also have `app.kubernetes.io/managed-by=example-platform`. Every
applicable filter must pass. An empty list adds no filtering.

Both type fields must be set or both omitted. Duplicate entries for the same
version and kind, including a second global entry, are rejected. Filters apply
to roots and association targets, not intermediate owners. Label changes
re-evaluate selection. These filters do not change RBAC or remove objects from
the watch cache.

## Metric output

| Under `exporter.metrics` | Default | Effect |
| --- | --- | --- |
| `workloadSelector` | `{}` | Selects roots that publish status and creation time. Pod info remains. |
| `excludeStatuses` | `[Suspending, Resuming]` | Omits these status gauges. Set `[]` to emit all nine phases. |
| `labelRules` | `[]` | Computes labels for Pod info or workload status. |
| `podWorkloadAssociation` | `null` | Associates a bare Pod's metric identity with a workload in its namespace. |

### Metric delivery

| Under `exporter` | Default | Effect |
| --- | --- | --- |
| `serviceMonitor.enabled` | `false` | Creates a ServiceMonitor for an existing Prometheus Operator. |
| `serviceMonitor.additionalLabels` | `{}` | Labels used by Prometheus to select the ServiceMonitor. |
| `serviceMonitor.interval`, `serviceMonitor.scrapeTimeout` | `` | Empty values inherit Prometheus settings. Use durations such as `30s` and `10s`. |
| `metricsPort` | `8080` | Shared listener, container, probe and Service port. |
| `service.annotations` | `{}` | Adds annotations to the metrics Service. |

Only Prometheus pull is wired into the current runtime. `/metrics`, `/healthz`
and `/readyz` share the listener. Native OTLP push is unsupported. A separately
configured OTel Collector can scrape `/metrics` and forward to an OTLP backend.
The endpoint has no built-in authentication. The chart does not provide a
NetworkPolicy; configure network access for your monitoring system.

The Service publishes unready addresses so diagnostics remain reachable during
watch failures. Readiness still requires healthy inventory and synchronized
required watches. Scrape each exporter endpoint separately rather than a single
load-balanced address when comparing replicas. Scrape success does not establish
[observation validity](../keps/0001-metrics-exporter/README.md#reading-observation-validity).

#### Prometheus Operator

If Prometheus Operator and its ServiceMonitor CRD are already installed, enable
`exporter.serviceMonitor.enabled`. The chart creates a ServiceMonitor in the
exporter's namespace, selecting its Service and named `metrics` port. It does
not install Prometheus Operator, Prometheus or their CRDs.

The ServiceMonitor sets `honorLabels: true`. The exporter's `namespace` label is
the workload's namespace, and it must not be replaced by the namespace of the
exporter Pod. Without it, Prometheus moves the value to `exported_namespace`.
Series without their own `namespace`, such as Go runtime metrics, keep the
exporter Pod's namespace. If you write your own ServiceMonitor, set
`honorLabels: true`. In a Prometheus `scrape_config`, set `honor_labels: true`.
The Prometheus resource must not set `overrideHonorLabels: true`, which makes
the Prometheus Operator ignore `honorLabels` on every ServiceMonitor.

Prometheus must select the ServiceMonitor's namespace and labels through
`serviceMonitorNamespaceSelector` and `serviceMonitorSelector`. For example,
if its selector requires `release: prometheus`, use:

```yaml
exporter:
  serviceMonitor:
    enabled: true
    additionalLabels:
      release: prometheus
```

Use the label required by your Prometheus installation. Creating a ServiceMonitor
does not configure a standalone Prometheus server. See
[Prometheus Operator discovery](https://prometheus-operator.dev/docs/platform/troubleshooting/#troubleshooting-servicemonitor-changes).

### Status phases

`excludeStatuses` removes phases from `dsx_workload_status`. The default omits
`Suspending` and `Resuming`, reducing each workload's status gauges from nine to
seven. Sandbox is the only bundled definition that maps these transitions.
To observe Sandbox transitions or custom definitions that use these phases,
remove them from the exclusion list.

Set `excludeStatuses: []` to emit all nine phases, or provide a list that replaces
the default exclusions. In a Helm values file, use `exporter.metrics.excludeStatuses`.

```yaml
exporter:
  metrics:
    excludeStatuses: []
```

Names are case-sensitive: `Initializing`, `Running`, `Completed`, `Failed`,
`Degraded`, `Suspended`, `Suspending`, `Resuming` and `Undefined`. Unknown names
fail startup. Each enabled phase remains a zero-or-one gauge. Exclusions affect
only metric output; status evaluation and observation health are unchanged.

### Configured labels

`labelRules` targets only `dsx_workload_pod_info` and `dsx_workload_status`.
Sources are `workload` and `pod`; status accepts only `workload`. Each expression
receives the full supplied object in `object`, without Kubernetes calls or
mutation. There are no evaluation event filters or first-value caching modes.

```yaml
exporter:
  metrics:
    labelRules:
      - name: team
        metrics: [dsx_workload_pod_info, dsx_workload_status]
        sources: [workload]
        expression: |
          has(object.metadata.labels) && "example.com/team" in object.metadata.labels
            ? object.metadata.labels["example.com/team"] : ""
```

For a Job root, the evaluator receives `{"object": <full Job object>}`. Its
relevant fields are:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: train
  namespace: workloads
  labels:
    example.com/team: research
```

The expression returns `"research"`, producing `team="research"`.
To read a Pod's scheduler instead, use `metrics: [dsx_workload_pod_info]`,
`sources: [pod]` and `expression: object.spec.schedulerName`. The evaluator then
receives the full Pod, not its workload root.

Rules run in list order. Within a rule, the first nonempty source value wins.
The last successful rule for a duplicate label name wins. An empty rule result
does not erase an earlier rule's value. Source errors remain reported even when
a fallback succeeds. Runtime can retain the entire previous Pod or status result
as invalid after a label error; successful labels are not independently published.
With no prior value, the affected output can be absent.

Rules can add any valid custom label. A nonempty rule value also replaces an
unprotected built-in label, such as `component`, `component_instance` or
`replica_group_id`. Empty results keep the inferred value.

The collector preserves these names when they are built-in labels of the metric:

| Purpose | Protected labels |
| --- | --- |
| Identity and joins | `namespace`, `workload_name`, `workload_uid`, `workload_group`, `workload_version`, `workload_kind`, `pod_name`, `pod_uid` |
| Evaluation result | `phase`, `owner_resolution`, `observation_valid`, `karta`, `karta_source` |
| Collection health | `scope`, `resource_group`, `resource_version`, `resource_kind`, `reason` |

Protection applies to built-in collisions. For example, `phase` cannot replace
the status metric's phase, but it can be a custom label on Pod info.
This rule reads the Pod's `platform.example.com/role` label. A value of
`worker` produces `component="worker"`. Pods without that label keep their
inferred component.

```yaml
exporter:
  metrics:
    labelRules:
      - name: component
        metrics: [dsx_workload_pod_info]
        sources: [pod]
        expression: |
          has(object.metadata.labels) &&
          "platform.example.com/role" in object.metadata.labels
            ? object.metadata.labels["platform.example.com/role"] : ""
```

Names starting with `__` are rejected. Syntax, statically known result-type and expression-size errors reject
startup. Dynamic type, result-size and cost errors happen during evaluation and
do not stop the process. See the [resolver](../../dsx-workload-exporter/pkg/labelresolver/README.md).

### Workload metrics and Pod association

Some platforms create bare Pods and a separate workload object that refers back
to them. For example, Pod `worker-0` has UID `pod-123` and label
`platform.example.com/workload-name=train-a`. Workload `train-a` is in the same
namespace and has an owner reference naming Pod `worker-0` with UID `pod-123`.
Neither object has a controller owner. The Pod also has
`app.kubernetes.io/managed-by=example-platform`.

Association is disabled by default. The target type must already exist and have
a selected Karta definition. It must pass resource and object selection. The
exporter checks its owner references against the Pod's name and current UID.
A Job-owned Pod keeps its normal Job root. The exporter uses the existing
full-object watch cache, makes no per-Pod GET and changes no owner references.

The resulting Pod row uses the Workload's UID as `workload_uid` and keeps
`pod_uid="pod-123"`. Its workload identity and `sources: [workload]` expressions
use the target Workload. `sources: [pod]` expressions still receive the Pod.
Component inference uses the physical ownership tree. Pod and target updates
re-check the association.

`workloadSelector` selects roots that publish status and creation time.
It defaults to all roots and does not remove Pod info. In this example, only
the bare Pods carry the workload-name label, so their extra workload rows are
suppressed. The target Workload still publishes its own rows.

Filtering and association are independent. A missing or invalid target leaves
Pod info under the original Pod UID with `observation_valid="false"` and a
health row with reason `unresolved_owner`. Restoring a valid target clears the
failure. A failed target watch immediately invalidates existing associations.
The last known target UID may remain until the next Pod reconciliation falls
back to the Pod UID. Watch recovery requeues affected Pods.

<details>
<summary>Complete Helm values for Pod association</summary>

This example combines association, root filtering and a
[component label rule](#configured-labels). Replace the API type, labels and
namespace with those used by your platform. Both Pods and targets must have
`environment=production` and `app.kubernetes.io/managed-by=example-platform`.
Only the Pods carry `platform.example.com/workload-name`.

The workload CRD, its controller and its Karta definition must already be
installed; the exporter chart does not install them. Save these values as
`pod-association-values.yaml`:

```yaml
operator:
  enabled: false
exporter:
  enabled: true
  selection:
    namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: workloads
    rootSelectors:
      - selector:
          matchLabels:
            environment: production
      - apiVersion: scheduling.example.org/v1
        kind: Workload
        selector:
          matchLabels:
            app.kubernetes.io/managed-by: example-platform
  metrics:
    workloadSelector:
      matchExpressions:
        - key: platform.example.com/workload-name
          operator: DoesNotExist
    podWorkloadAssociation:
      podSelector:
        matchLabels:
          app.kubernetes.io/managed-by: example-platform
        matchExpressions:
          - key: platform.example.com/workload-name
            operator: Exists
      target:
        apiVersion: scheduling.example.org/v1
        kind: Workload
        nameFromLabel: platform.example.com/workload-name
    labelRules:
      - name: component
        metrics: [dsx_workload_pod_info]
        sources: [pod]
        expression: |
          has(object.metadata.labels) &&
          "platform.example.com/role" in object.metadata.labels
            ? object.metadata.labels["platform.example.com/role"] : ""
```

From this checkout, use an exporter image built from the same checkout:

```sh
helm upgrade --install workload-exporter ./charts/karta \
  --namespace monitoring --create-namespace \
  -f pod-association-values.yaml \
  --set exporter.image.repository=ghcr.io/example/dsx-workload-exporter \
  --set-string exporter.image.tag=YOUR_TAG
```

Replace the image placeholders. For an existing release, keep its release name,
namespace, values and operator setting. The example disables the operator.
See [installation](installation.md) for image and scrape setup.

</details>

### Submitter admission

Optional caller capture is separate from the exporter. Its admission policy,
Helm settings and annotation extraction example are documented in
[submitter capture](../design/metrics-exporter/submitter-capture.md). It is disabled
by default and is not needed for the exporter installation guide.

## Deployment and permissions

| Under `exporter` | Default | Effect |
| --- | --- | --- |
| `rbac.workloadRules` | All API groups and resources | Grants workload read access, including Secrets. Set explicit rules to limit access. |
| `resources` | Requests: `500m` CPU, `512Mi` memory. Limits: `2` CPU, `4Gi` memory. | Sizes the exporter container. Watch count, object size and scrape frequency affect usage. |
| `image.repository` | `ghcr.io/dsx-ai-factory/workload-map/dsx-workload-exporter` | Exporter image repository. |
| `image.tag`, `image.pullPolicy` | ``, `IfNotPresent` | An empty tag uses the chart appVersion. |
| `nodeSelector`, `affinity`, `tolerations` | `{}`, `{}`, `[]` | Places the exporter Pod. |
| `podLabels`, `podAnnotations` | `{}` | Adds Pod metadata. Selector labels and `checksum/config` are reserved. |

The default workload ClusterRole grants `list` and `watch` on every API
group/resource, including Secrets. Runtime exclusions do not reduce these grants.
Replace `exporter.rbac.workloadRules` with the roots and all intermediate owners
you need. For Deployment attribution this includes Deployments and ReplicaSets;
for JobSet it includes JobSets and Jobs. Pod, Namespace and enabled cluster-Karta
reads are separate built-in rules. RBAC rules use no API version field.

The chart creates a direct ClusterRoleBinding to the exporter's ServiceAccount.
It does not configure ClusterRole aggregation. Namespace selectors restrict
observation, not authorization. Adding a narrow RoleBinding does not remove the
existing cluster-wide grant.

The exporter uses one replica and its own ServiceAccount. Global
`imagePullSecrets` applies to it. `global.fipsMode` also controls the exporter;
see [FIPS modes](../configuration/karta-operator/run-in-fips-mode.mdx#set-the-mode). Operator settings such as `replicaCount`,
`resources`, `ports`, `serviceAccount`, `extraArgs` and `podAnnotations` do not
configure the exporter. `fullnameOverride` changes its resource-name prefix.

For an unreleased checkout, set `image.repository` and `image.tag` to an image
built from that checkout and accessible to cluster nodes. The chart appVersion
does not guarantee that an exporter image has been published. See
[development](../../dsx-workload-exporter/README.md#development) for build commands.

Helm writes the runtime settings to a ConfigMap and mounts its file at
`/etc/dsx-workload-exporter/config.yaml`. It uses the same groups as a
standalone file. A change to the ConfigMap checksum rolls the Pod. The
exporter reads the file at startup.

### Watches and owner reads

The controller watches Pods, selected definition kinds and required kinds.
Discovery asks its reader for missing owner metadata. The reader acquires shared
metadata watch coverage for those ancestors. Evaluated roots use full-object
coverage. A replacement full-object watch must synchronize before replacing the
metadata watch. Denied LIST/WATCH is not silently replaced by GET-only observation.
See the [controller flow](../../dsx-workload-exporter/pkg/controller/README.md).

Kubernetes may warn when a watched API version is deprecated. The exporter logs
each distinct API warning once per process. Select a Karta definition using the
supported API version to address the warning. Enabling the catalog does not
replace a deprecated version selected by a cluster definition.

## Advanced tuning and profiling

| Under `exporter.tuning` | Default | Effect |
| --- | --- | --- |
| `workers.default` | `4` | Sets the default concurrency for each Pod and workload queue. |
| `workers.pods`, `workers.workloads` | `0`, `0` | Overrides Pod or workload worker counts. Zero inherits `workers.default`. |
| `apiClient.qps`, `apiClient.burst` | `20`, `40` | Sets the shared Kubernetes API request rate and burst. |
| `apiClient.requestTimeout` | `30s` | Bounds resource discovery and each LIST page. |
| `goMemoryLimit` | `auto` | Sets the container's `GOMEMLIMIT`. Accepts `auto`, a number in GiB from 0.001 to 1024, or an empty string to leave it unset. |
| `pprof.enabled`, `pprof.port` | `false`, `6060` | Serves Go pprof at `127.0.0.1:<port>` in the Pod. Use `kubectl port-forward` to connect. |

The worker counts apply independently to each queue. API requests share one
rate limiter across queues.

With `goMemoryLimit: auto`, Helm sets the Go soft memory limit to 90% of
`resources.limits.memory`. The chart accepts Kubernetes memory quantities that
fit in 64-bit bytes. An absent or zero container limit leaves `GOMEMLIMIT`
unset. The remaining 10% allows for memory outside the Go runtime.

Near the soft limit, Go spends more CPU on garbage collection, up to about half
the available CPU time. A live working set can exceed the soft limit and
eventually the container limit, which can trigger an OOM kill. If memory stays
at the limit while CPU rises, increase `resources.limits.memory`. With `auto`,
the Go limit follows.

### Label evaluation limits

The exporter bounds the cost of each CEL label evaluation, the expression
length and the returned string.

| Under `exporter.metrics.labelEvaluationLimits` | Default | Effect |
| --- | --- | --- |
| `costLimit` | `10000` | CEL cost units per evaluation. |
| `maxExpressionBytes` | `4096` | Maximum expression size in bytes. |
| `maxResultBytes` | `1024` | Maximum returned string size in bytes. |

## Standalone binary and precedence

Use `selection`, `metrics`, `tuning` and `kartaDefinitionSource` at the YAML
root. A standalone file omits the Helm `exporter` wrapper. For example, this
file starts two Pod workers and eight workload workers.

```yaml
tuning:
  workers:
    pods: 2
    workloads: 8
```

For a standalone process, set the memory limit with `GOMEMLIMIT` and configure
profiling with `--pprof-bind-address`. Helm uses `tuning.goMemoryLimit` and
`tuning.pprof` to set them on the exporter container. Keep chart settings such as the image,
placement and RBAC in the Helm values file. Unknown YAML keys fail startup.
Selectors, definition sources and `metrics.excludeStatuses` use YAML only.

Explicit flags override environment variables, then YAML, then binary defaults.
An empty numeric override is an error. `--config=""` skips an environment-provided
path. Zero CEL limits use the defaults above; negative limits are rejected.

| YAML key | Flag | Environment variable suffix |
| --- | --- | --- |
| Config path | `--config` | `CONFIG` |
| `tuning.workers.default` | `--workers` | `WORKERS` |
| `tuning.workers.pods` | `--pod-workers` | `POD_WORKERS` |
| `tuning.workers.workloads` | `--workload-workers` | `WORKLOAD_WORKERS` |
| `tuning.apiClient.requestTimeout` | `--request-timeout` | `REQUEST_TIMEOUT` |
| `tuning.apiClient.qps` | `--api-qps` | `API_QPS` |
| `tuning.apiClient.burst` | `--api-burst` | `API_BURST` |
| `metrics.labelEvaluationLimits.costLimit` | `--label-eval-cost-limit` | `LABEL_EVAL_COST_LIMIT` |
| `metrics.labelEvaluationLimits.maxExpressionBytes` | `--label-eval-max-expression-bytes` | `LABEL_EVAL_MAX_EXPRESSION_BYTES` |
| `metrics.labelEvaluationLimits.maxResultBytes` | `--label-eval-max-result-bytes` | `LABEL_EVAL_MAX_RESULT_BYTES` |

All suffixes use the `DSX_WORKLOAD_EXPORTER_` prefix. The listener is a separate
flag, `--metrics-bind-address`, default `:8080`; it has no YAML or environment
setting. `--version` prints the binary version.
`--pprof-bind-address` serves Go pprof profiles on a separate listener. It is
empty by default, which disables them; bind it to localhost.

Binary defaults match runtime values above except namespace and resource
selection: the binary selects all namespaces and excludes no root types.
The Helm namespace and ConfigMap/Secret exclusions are chart defaults.
