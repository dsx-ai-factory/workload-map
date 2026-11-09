<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter configuration

For a minimal Helm deployment, follow [installation](installation.md). The exporter
runs independently of the Karta operator. Settings below describe the current
chart and binary, not planned delivery modes.

## Helm values

The [`exporter` section in values.yaml](../../charts/karta/values.yaml#L13)
shows the full YAML configuration and its defaults. The chart writes runtime
settings into a ConfigMap mounted at `/etc/dsx-workload-exporter/config.yaml`.
A checksum annotation rolls the Pod when that configuration changes. The process
reads it at startup.

| Value under `exporter` | Default | Effect |
| --- | --- | --- |
| `enabled` | `false` | Deploy the exporter. |
| `image.repository` | `ghcr.io/dsx-ai-factory/workload-map/dsx-workload-exporter` | Image repository. |
| `image.tag` | `""` | Empty uses chart appVersion; override for an unreleased branch. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `metricsPort` | `8080` | Shared listener, container, probe and Service port. |
| `resources` | Requests: `500m` CPU, `512Mi` memory. Limits: `2` CPU, `4Gi` memory. | For large clusters, increase requests and limits based on measured CPU and memory usage. Watched object count, object size and scrape frequency affect usage. |
| `goMemLimit` | `auto` | Go runtime soft memory limit (`GOMEMLIMIT`). `auto` sets 90% of `resources.limits.memory`, so the garbage collector runs more often near the limit instead of the container being OOM-killed. A Go value such as `3600MiB` sets it directly; empty leaves it unset. |
| `nodeSelector`, `affinity` | `{}` | Exporter placement. |
| `tolerations` | `[]` | Exporter tolerations. |
| `podLabels`, `podAnnotations` | `{}` | Additional Pod metadata; selector labels and `checksum/config` are reserved. |
| `service.annotations` | `{}` | Additional Service annotations. |
| `serviceMonitor.enabled` | `false` | Create a ServiceMonitor for an existing Prometheus Operator. |
| `serviceMonitor.additionalLabels` | `{}` | Labels used by Prometheus to select the ServiceMonitor. |
| `serviceMonitor.interval`, `serviceMonitor.scrapeTimeout` | `""` | Empty inherits Prometheus settings. Use duration strings such as `30s` and `10s`. |
| `namespaceSelector` | Exclude `kube-system`, `kube-public`, `kube-node-lease` | Namespace label selection. Set `matchExpressions: []` to remove these exclusions. |
| `objectSelector`, `podSelector` | `{}` | Root and Pod output selection. |
| `resourceRules` | `[]` | Allowed root API types; empty means unrestricted. |
| `excludeResourceRules` | Core ConfigMaps and Secrets, all versions | Excluded root types. Set `[]` to remove these exclusions. |
| `ignoredOwnerKinds`, `requiredKinds` | `[]` | Owner boundaries and additional watched kinds. |
| `workers` | `4` | Default concurrency for each Pod/workload queue. |
| `leafWorkers` | `0` | Goroutines processing Pod queue jobs. Zero inherits `workers`. |
| `rootWorkers` | `0` | Goroutines processing workload queue jobs. Zero inherits `workers`. |
| `requestTimeout` | `30s` | Resource discovery and each LIST page timeout. |
| `apiQPS`, `apiBurst` | `20`, `40` | Shared Kubernetes client request rate and burst. |
| `rbac.workloadRules` | All API groups and resources | Read access, including Secrets; replace with explicit rules. |
| `labelRules` | `[]` | Extra labels, described below. |
| `genericLabelEvaluationLimits.costLimit` | `10000` | CEL cost units per evaluation. |
| `genericLabelEvaluationLimits.maxExpressionBytes` | `4096` | Maximum expression size. |
| `genericLabelEvaluationLimits.maxResultBytes` | `1024` | Maximum returned string size. |
| `kartaDefinitionSource.catalog.enabled` | `true` | Load bundled definitions. |
| `kartaDefinitionSource.cluster.enabled` | `true` | Watch cluster Karta definitions. |
| Each source's `include`, `exclude` | `[]` | Definition-name filters. |

The exporter uses one replica and its own ServiceAccount. Global
`imagePullSecrets` applies to it. `global.fipsMode` also controls the exporter;
see [FIPS modes](../FIPS.md#setting-the-mode). Operator settings such as `replicaCount`,
`resources`, `ports`, `serviceAccount`, `extraArgs` and `podAnnotations` do not
configure the exporter. `fullnameOverride` changes its resource-name prefix.

The default worker settings start four Pod workers and four workload workers.
These counts control queue processing, not the total goroutines or API requests.

`goMemLimit` is a soft limit. As memory nears it, the garbage collector runs
more often: memory stays under the limit, and the exporter uses more CPU. If the
live data alone is larger than the limit, the runtime caps garbage collection at
about half of the CPU and lets memory grow past it. The container is then
OOM-killed at `resources.limits.memory`. A working set that stays at the limit
while exporter CPU rises means the live data has outgrown it. Raise
`resources.limits.memory`; with `auto`, `GOMEMLIMIT` follows. The other 10% of
the limit covers memory outside the Go runtime.

For an unreleased checkout, set `image.repository` and `image.tag` to an image
built from that checkout and accessible to cluster nodes. The chart appVersion
does not guarantee that an exporter image has been published. See
[development](../../dsx-workload-exporter/README.md#development) for build commands.

## Standalone binary and precedence

Put runtime keys at the YAML root without `exporter:`. Omit chart-only keys such
as `enabled`, `image`, `metricsPort`, placement and `rbac`. Unknown YAML keys fail
startup. Selectors and definition sources use YAML only.

Explicit flags override environment variables, then YAML, then binary defaults.
An empty numeric override is an error. `--config=""` skips an environment-provided
path. Zero CEL limits use the defaults above; negative limits are rejected.

| YAML key | Flag | Environment variable suffix |
| --- | --- | --- |
| Config path | `--config` | `CONFIG` |
| `workers` | `--workers` | `WORKERS` |
| `leafWorkers` | `--leaf-workers` | `LEAF_WORKERS` |
| `rootWorkers` | `--root-workers` | `ROOT_WORKERS` |
| `requestTimeout` | `--request-timeout` | `REQUEST_TIMEOUT` |
| `apiQPS` | `--api-qps` | `API_QPS` |
| `apiBurst` | `--api-burst` | `API_BURST` |
| `genericLabelEvaluationLimits.costLimit` | `--generic-label-eval-cost-limit` | `GENERIC_LABEL_EVAL_COST_LIMIT` |
| `genericLabelEvaluationLimits.maxExpressionBytes` | `--generic-label-eval-max-expression-bytes` | `GENERIC_LABEL_EVAL_MAX_EXPRESSION_BYTES` |
| `genericLabelEvaluationLimits.maxResultBytes` | `--generic-label-eval-max-result-bytes` | `GENERIC_LABEL_EVAL_MAX_RESULT_BYTES` |

All suffixes use the `DSX_WORKLOAD_EXPORTER_` prefix. The listener is a separate
flag, `--metrics-bind-address`, default `:8080`; it has no YAML or environment
setting. `--version` prints the binary version.

Binary defaults match runtime values above except namespace and resource
selection: the binary selects all namespaces and excludes no root types.
The Helm namespace and ConfigMap/Secret exclusions are chart defaults.

## Selection

`namespaceSelector` selects namespace labels. `objectSelector` selects the
resolved root's labels. `podSelector` selects Pod attribution rows, not watches.
A Pod row requires both root and Pod selection. Workload status still describes
the full workload. Excluding a root does not promote its children to roots.
Namespace selection limits namespaced observation scope, not cluster-scoped
resources or RBAC authorization.

```yaml
exporter:
  namespaceSelector:
    matchLabels:
      kubernetes.io/metadata.name: workloads
    matchExpressions: []
  resourceRules:
    - apiGroups: ["apps"]
      apiVersions: ["v1"]
      resources: ["deployments"]
```

Resource rules match group, version and plural resource. Entries are alternatives;
all three fields must match within an entry. Each field needs a nonempty list.
`"*"` is a wildcard and `""` is the core API group. Exclusions win. Selectors,
malformed rules and unknown catalog names are validated before observation.

### Definition sources and owner boundaries

Source filters apply before precedence. Empty includes select all definition
names; exclusions win. A selected cluster Karta overrides the catalog for the
same group/kind. Cluster ties use oldest creation time, then name. Catalog ties
use Kubernetes version priority, then name. A source or selected-definition
error is not absence and does not trigger fallback.

Both sources are enabled by default. Set a source's `enabled` to `false` to
disable it. Cluster watching requires the Karta CRD, even when no Karta objects
exist. Bundled definitions provide built-in workload support without those objects.

`ignoredOwnerKinds` contains exact GVK strings, such as
`example.com/v1/PlatformWrapper`. Traversal stops before that owner. The last
verified object below it becomes the root. A definition on an intermediate owner
does not stop traversal. Missing owners below the boundary still fail.
`requiredKinds` uses the same format and adds watched kinds, including types
that have no selected definition or Pods yet.

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

## Configured labels

`labelRules` targets only `dsx_workload_pod_info` and `dsx_workload_status`.
Sources are `workload` and `pod`; status accepts only `workload`. Each expression
receives the full supplied object in `object`, without Kubernetes calls or
mutation. There are no evaluation event filters or first-value caching modes.

```yaml
exporter:
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

Use custom names that do not collide with built-in labels. The resolver's
`Apply` helper supports overrides, but the current collector appends built-ins
last, so built-in values win in exported metrics. Names starting with `__` are
rejected. Syntax, statically known result-type and expression-size errors reject
startup. Dynamic type, result-size and cost errors happen during evaluation and
do not stop the process. See the [resolver](../../dsx-workload-exporter/pkg/labelresolver/README.md).

## Submitter admission

Optional caller capture is separate from the exporter. Its admission policy,
Helm settings and annotation extraction example are documented in
[submitter capture](../design/metrics-exporter/submitter-capture.md). It is disabled
by default and is not needed for the exporter installation guide.

## Metric delivery

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

### Prometheus Operator

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

## Deployment and permissions

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
