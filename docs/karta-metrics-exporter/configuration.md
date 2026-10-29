<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta metrics exporter configuration

Proposed configuration for [KEP-0001](../keps/0001-metrics-exporter/README.md).
The exporter runs separately from the Karta controller. Helm configures its
definitions, observation scope, configured labels and metric delivery.

## Helm values

```yaml
exporter:
  enabled: true # Deploy the exporter; default false.
  image:
    tag: "<exporter-tag>" # Exporter image version.
  metricsExporter: prometheus # Default: prometheus. Set otlp for native push.
  otlp: # Used only when metricsExporter is otlp.
    protocol: http/protobuf # Or grpc.
    endpoint: https://metrics.example.com:4318/v1/metrics
    interval: 30s
    timeout: 10s
    headersSecretRef: {name: karta-otlp-auth, key: headers} # Optional.
    tls: # Optional custom CA and mutual TLS.
      caSecretRef: {name: karta-otlp-tls, key: ca.crt}
      clientCertSecretRef: {name: karta-otlp-tls, key: tls.crt}
      clientKeySecretRef: {name: karta-otlp-tls, key: tls.key}
  http:
    port: 8080 # /healthz and /readyz; also /metrics in Prometheus mode.
  rbac:
    # Read workload roots and intermediate owners; the chart adds get/list/watch.
    # This list replaces the defaults. Include every selected workload type and its owners.
    workloadRules:
      - apiGroups: ["apps"]
        resources: ["deployments", "replicasets", "statefulsets"]
      - apiGroups: ["batch"]
        resources: ["jobs", "cronjobs"]
      - apiGroups: ["jobset.x-k8s.io"]
        resources: ["jobsets"]
  # Definitions describe each workload type. Include/exclude lists use definition names.
  kartaDefinitionSource:
    catalog:
      enabled: true # Built-in definitions; default false.
      include: []
      exclude: [] # Default empty; add core-pod-v1 to skip bare Pods.
    cluster:
      enabled: true # Cluster-installed Karta definitions; default true.
      include: []
      exclude: []
  # Watch Pods and workload objects only in matching namespaces.
  namespaceSelector: # Default null or {}: all namespaces.
    matchExpressions:
      - key: example.com/tenant # Include namespaces carrying this label.
        operator: Exists
      - key: kubernetes.io/metadata.name
        operator: NotIn
        values: [kube-system, karta-system] # Skip these system namespaces.
  # Match the workload root's metadata.labels; retain owner reads for traversal.
  objectSelector: # Default null or {}: all resolved workload roots.
    matchExpressions:
      - key: example.com/purpose
        operator: NotIn
        values: [reservation]
  # Match each Pod's metadata.labels to select its exported row.
  # All Pods in selected namespaces stay watched.
  podSelector: # Default null or {}: all Pod attribution rows.
    matchExpressions:
      - key: example.com/purpose
        operator: NotIn
        values: [reservation]
  resourceRules: [] # Limit root API types; empty means no restriction.
  excludeResourceRules: [] # Exclusions take precedence.
  # Stop before these GVKs; the last verified object below becomes the workload root.
  ignoredOwnerKinds: # Default [].
    - example.com/v1/PlatformWrapper
  requiredKinds: [] # Optional expected APIs; failed checks mark dependent observations unhealthy.
    # Example: ["jobset.x-k8s.io/v1alpha2/JobSet"]
  labelRules: [] # Extra labels for Pod info and workload status.
  # Example entry: read example.com/team from the workload object.
  # - name: team
  #   metrics: [karta_pod_workload_info, karta_workload_status]
  #   sources: [workload]
  #   expression: |
  #     has(object.metadata.labels) && "example.com/team" in object.metadata.labels
  #       ? object.metadata.labels["example.com/team"] : ""
  # Bounds for label expressions, evaluation and returned values.
  genericLabelEvaluationLimits:
    costLimit: 10000
    maxExpressionBytes: 4096
    maxResultBytes: 1024
  metrics:
    workloadPhases: ["all"] # Emit every workload phase as a 0/1 series.
  # Let Prometheus Operator discover and scrape the exporter.
  serviceMonitor:
    enabled: true # Create the optional ServiceMonitor; default false.
    interval: 30s # How often Prometheus scrapes metrics.
    scrapeTimeout: 25s # Maximum duration of one scrape.
```

`exporter.enabled` defaults to `false`. The example enables it and the built-in
catalog. `http.port` serves `/healthz` and `/readyz` in both delivery modes, plus
`/metrics` in Prometheus mode. It also sets the container, probe and Service ports.

## Selection

| Setting under `exporter` | Selects |
| --- | --- |
| `namespaceSelector` | Namespace labels; use the built-in `kubernetes.io/metadata.name` label with `In` to include names or `NotIn` to exclude them. |
| `objectSelector` | Resolved workload roots by their labels. |
| `podSelector` | Pods whose attribution rows are exported, using each Pod's labels. |
| `resourceRules` / `excludeResourceRules` | Root API types, using group, version and plural resource name. Exclusions win. |
| Definition-source `include` / `exclude` | Definition names. Empty includes mean all; exclusions win. |

Use [Kubernetes label selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors)
to match each object's own labels. All conditions must match; `NotIn` also matches
missing labels.

Namespace selection limits watches and owner reads to the selected namespaces.

`objectSelector` selects workload roots. `podSelector` selects Pod rows; both must
match for a Pod row to appear. Workload status covers the full workload; joined
usage covers only included Pods. Excluding a root excludes its Pods too, without
treating its children as new roots.

Scope changes remove rows outside the selection. A failed refresh never expands
the scope.

### Watches and owner reads

The runtime always watches Pods in selected namespaces. It starts watches for
workload kinds selected by Karta definitions so workloads with no Pods are found.
Resolving a Pod also starts a shared watch for its root resource and namespace
when no watch already covers them. This applies even without a Karta definition.

Intermediate owners use metadata GETs and a shared cache. Traversing
Pod -> Job -> JobSet does not start a Job watch. Jobs can still be watched when
selected by a Karta definition or discovered as roots elsewhere.

Pod and root events trigger reconciliation. Periodic revalidation checks the
intermediate owners and renews verification. An intermediate change can remain
undetected until that check or another reconciliation. Failed checks or expired
verification invalidate dependent observations; a scrape does not renew them.

### Workload API types

```yaml
exporter:
  resourceRules:
    - apiGroups: ["batch"]
      apiVersions: ["v1"]
      resources: ["jobs", "cronjobs"]
    - apiGroups: ["jobset.x-k8s.io"]
      apiVersions: ["*"]
      resources: ["jobsets"]
  excludeResourceRules:
    - apiGroups: ["batch"]
      apiVersions: ["v1"]
      resources: ["cronjobs"]
```

A root matches its label selector, at least one inclusion rule when configured,
and no exclusion rule. Group, version and resource must all match within a rule;
list entries are alternatives. `"*"` matches any value; `""` is the core API group.
Core Pods use group `""`, version `v1` and resource `pods`.

The fields follow Kubernetes [MatchResources](https://kubernetes.io/docs/reference/kubernetes-api/definitions/match-resources-v1-admissionregistration/),
without admission operations or subresources. Each supplied rule needs nonempty
group, version and resource lists. Invalid selectors, malformed rules and unknown
catalog names reject configuration. Other served API versions do not create
duplicate observations.

### Definition sources and owner boundaries

Source filters apply before precedence. A selected cluster Karta overrides a
selected catalog definition for the same group/kind. Disabled sources contribute
none. Ties use oldest creation time, then name.

An enabled catalog includes `core-pod-v1`, so a bare Pod is a workload of its own,
as it is with a selected cluster Pod Karta. Exclude `core-pod-v1` to skip bare Pods.
`objectSelector` also applies to those roots.

`ignoredOwnerKinds` stops the owner walk before an exact GVK. The last verified
object below it becomes the root; missing owners below that boundary still fail.
A Karta definition on an intermediate owner does not by itself stop the walk.
An ignored owner can still be selected independently. `requiredKinds` checks
remain active; failed checks invalidate dependent observations.

## Configured labels

`labelRules` adds named labels to `karta_pod_workload_info` and
`karta_workload_status`. Each rule provides metric names, ordered sources and a
CEL expression. Sources are `workload` and `pod`. Status rules cannot use `pod`.
Expressions read the supplied full object through `object`; they do not call
Kubernetes or mutate it. Guard optional fields with `has(...)`.

This rule reads a team from the workload object:

```yaml
exporter:
  labelRules:
    - name: team
      metrics: [karta_pod_workload_info, karta_workload_status]
      sources: [workload]
      expression: |
        has(object.metadata.labels) && "example.com/team" in object.metadata.labels
          ? object.metadata.labels["example.com/team"] : ""
```

For JobSet `train`, the evaluator receives the full object. Its relevant fields are:

```yaml
apiVersion: jobset.x-k8s.io/v1alpha2
kind: JobSet
metadata:
  name: train
  namespace: team-a
  labels:
    example.com/team: research
```

The expression returns `"research"` for the configured `team` label.

The command validates rule names, metric/source combinations and CEL at startup.
The CEL evaluator is implemented. Resolver execution and exporting the resolved
labels are not implemented yet. Event selection and cache modes remain part of
the [proposed runtime design](../design/metrics-exporter/README.md#evaluate-extra-metric-labels);
the current label-rule configuration does not expose them.

The chart renders `labelRules` and `genericLabelEvaluationLimits` into the mounted
config file. Rules default to `[]`. Limits default to 10000 cost units, 4096
expression bytes and 1024 result bytes. Zero limits use these defaults.

See the [evaluator configuration](../../karta-metrics-exporter/pkg/labelevaluator/README.md#configuration)
for a complete example, flags, environment variables and precedence.

## Submitter admission

`admissionPolicies.submitter` captures direct Kubernetes callers. Exporter label
extraction is configured separately, as described in [Configured labels](#configured-labels).

Capture uses a `MutatingAdmissionPolicy` and its binding. It runs in the API
server without a Karta operator. MAP is
[stable in Kubernetes 1.36+](https://kubernetes.io/docs/reference/access-authn-authz/mutating-admission-policy/);
[1.34-1.35 requires beta feature/API enablement](https://v1-35.docs.kubernetes.io/docs/reference/access-authn-authz/mutating-admission-policy/).

This example captures Deployments and Jobs in opted-in namespaces:

```yaml
admissionPolicies:
  submitter:
    enabled: true # Capture on CREATE; default false.
    annotation: karta.run.ai/submitted-by # Annotation key that receives the caller.
    apiVersion: "" # MAP version, v1 or v1beta1; empty detects it from the cluster.
    resources: # Group, version and plural resource; the chart sets operations and scope.
      - apiGroups: ["apps"]
        apiVersions: ["v1"]
        resources: ["deployments"]
      - apiGroups: ["batch"]
        apiVersions: ["v1"]
        resources: ["jobs"]
    excludeResources: [] # Same form; exclusions win.
    namespaceSelector: # Default: the opt-in label below. Set matchLabels: null to drop it.
      matchLabels:
        karta.run.ai/capture-submitter: "true"
      matchExpressions:
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values: [kube-system, karta-system]
    objectSelector: {} # Workload labels; {} matches all.
    matchConditions: [] # Optional CEL conditions on the CREATE request.
```

Capture follows [native Kubernetes matching](https://github.com/kubernetes/kubernetes/blob/v1.36.0/staging/src/k8s.io/api/admissionregistration/v1/types.go#L554-L642).
It selects namespaced CREATE requests. Exclusions win and all conditions must
pass. Objects with a controller `ownerReference` are skipped: a controller that
copies a parent's annotations onto a child is not the submitter. Objects the
annotation would push over the 256 KiB annotation size limit are skipped. An empty
namespace selector matches all namespaces. Helm merges maps with the defaults, so
match all namespaces with `matchLabels: null`, not `{}`. Condition names starting
with `karta-` are reserved. Direct Pod capture uses
group `""`, version `v1`, resource `pods`. Subresources cannot be listed. These
settings do not filter exporter watches.

Mutation fills a missing `annotation` key, `karta.run.ai/submitted-by` by
default, with the caller's username. The key must be a valid annotation key.
When it changes, update the `labelRules` expression to read the new key from
`object.metadata.annotations`. A service account appears as
`system:serviceaccount:<namespace>:<name>`.
A supplied value is kept, so the annotation is a hint, not proof of identity.

Capture is best effort and never rejects a request. The policy uses
`failurePolicy: Ignore`: if it is unavailable, or a CEL expression fails to
evaluate, the request is admitted without the annotation. Administrators control
namespace labels and policy settings.

With `apiVersion` empty, the chart detects the API and fails if the cluster does
not serve it. `helm template` and GitOps renderers see no cluster APIs, so set
`apiVersion` when using them.

To enable capture on an existing release, upgrade with
`--reset-then-reuse-values` (Helm 3.14+), not `--reuse-values`. `--reuse-values`
skips new chart defaults, so a missing `namespaceSelector` would capture in every
namespace.

## Metric delivery

| Mode | Configuration | Requirements |
| --- | --- | --- |
| Prometheus pull | `metricsExporter: prometheus` | Prometheus scrapes `/metrics`. ServiceMonitor is optional. |
| Native OTLP push | `metricsExporter: otlp` | An OTLP receiver; endpoint and protocol under `otlp`. |
| Collector bridge | Prometheus mode, scraped by a Collector | The Collector forwards to the chosen backend. |

Combined native pull and push is deferred. CPU, memory and GPU measurements come
from other exporters; the [query guide](README.md) shows how to join them.

### Prometheus

![Karta provides workload identity; Prometheus joins it with resource measurements for dashboards and other consumers.](../keps/0001-metrics-exporter/metrics-architecture.png)

`serviceMonitor.enabled: true` creates a ServiceMonitor. Its CRD must be installed
and the Prometheus selector must match it. The Prometheus Operator turns it into
scrape configuration. Without the Operator, Prometheus uses its own scrape configuration.
See [ServiceMonitor discovery](https://prometheus-operator.dev/docs/developer/getting-started/#using-servicemonitors).

Prometheus discovers and scrapes each exporter endpoint separately. A single
load-balanced Service address can hide conflicting observations during a rollout.
Target identity remains available until query aggregation. The HTTP endpoint has
no built-in authentication; access is restricted by network policy or an
authenticated proxy.

### Native OTLP

In OTLP mode, `serviceMonitor.enabled` is `false`. An OTel periodic reader sends
the current metric snapshot, including unchanged values, to `otlp.endpoint`.
HTTP uses `POST /v1/metrics`; gRPC uses `MetricsService.Export`.
For gRPC, the endpoint is `https://metrics.example.com:4317`, without `/v1/metrics`.

`interval` and `timeout` accept positive whole-millisecond durations. The timeout
bounds the collection/export cycle and the client request. Unsupported protocols,
invalid durations and invalid endpoints reject startup.

Authentication and TLS Secrets live in the exporter's namespace. The chart mounts
only the referenced TLS keys read-only at `/etc/karta/otlp`. Kubernetes supplies
the files; no PVC or Secrets API read permission is needed. Missing referenced
Secrets or keys prevent startup.

`headersSecretRef` supplies comma-separated `key=value` headers through a generated
Secret-backed environment variable. HTTPS uses system trust by default. A custom
CA is optional; mutual TLS requires both client certificate and key. Secret
changes require an exporter restart. See [Secret projection](https://kubernetes.io/docs/concepts/storage/projected-volumes/)
and [OTLP settings](https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/).

Removed objects stop appearing in exports. The backend needs expiry or stale-series
handling; omission is not an immediate delete command. Delivery success does not
certify observation validity. See the [OTLP gauge model](https://opentelemetry.io/docs/specs/otel/metrics/data-model/#gauge).

### Collector bridge

An [OTel Collector](https://opentelemetry.io/docs/platforms/kubernetes/helm/collector/)
can scrape `/metrics` and forward the samples. PromQL joins require a compatible
query backend. The Collector and backend control label translation, deletion and
freshness.

## Deployment and permissions

| Value under `exporter` | Purpose |
| --- | --- |
| `rbac.workloadRules` | Read access for workload kinds and intermediate owners. |
| `resources` | CPU and memory requests/limits. |
| `nodeSelector`, `tolerations`, `affinity` | Pod placement. |
| `networkPolicy` | Access to the metrics endpoint. |

The main Helm example includes `rbac.workloadRules`. These rules use API groups
and plural resource names, as in Kubernetes RBAC. The chart supplies `get`, `list`
and `watch` for every listed resource. These grants remain broader than runtime
use: intermediate owners need metadata GETs for traversal, while selected
workload kinds and discovered roots also use LIST/WATCH. See
[watches and owner reads](#watches-and-owner-reads). API versions are selected by
`resourceRules`, not RBAC.

Include the root and every intermediate owner needed for attribution:

| Workload | Workload rules | Why |
| --- | --- | --- |
| Deployment | `apps`: `deployments`, `replicasets` | Pods belong to ReplicaSets, which belong to the Deployment. |
| JobSet | `jobset.x-k8s.io`: `jobsets`; `batch`: `jobs` | Jobs connect the Pods to the JobSet. |
| LeaderWorkerSet | `leaderworkerset.x-k8s.io`: `leaderworkersets`; `apps`: `statefulsets` | StatefulSets connect the Pods to the LeaderWorkerSet. |
| RayJob | `ray.io`: `rayjobs`, `rayclusters`; `batch`: `jobs` | Read the RayCluster and any launcher Job created by the RayJob. |

The main example covers Deployments, StatefulSets, Jobs, CronJobs and JobSets.
Keep permissions aligned with the selected definitions and API types, including
intermediate owners. The list replaces the default workload rules. Pod, Namespace
and enabled definition-source reads are
separate built-in rules. Namespace reads are needed to evaluate namespace labels.
Using an existing ServiceAccount does not disable the chart's role binding.

To add a workload type without replacing the Helm list, an administrator can
create a separate ClusterRole:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: karta-exporter-trainingruns
  labels:
    karta.run.ai/exporter-rbac: "true"
rules:
  - apiGroups: ["example.com"]
    resources: ["trainingruns"]
    verbs: ["get", "list", "watch"]
```

Replace the example group and resource with the workload API. Kubernetes merges
this rule into every exporter role selecting that label. The label is not specific
to one Helm release. Keep extension rules read-only; aggregation itself does not
restrict their verbs.

Namespace selection reduces watches, not authorization. Restricting access to named
namespaces requires Roles/RoleBindings in place of a cluster-wide workload binding;
adding a narrow binding does not remove existing grants. Namespace objects still
need cluster-scoped read access.

This follows [Kubernetes RBAC aggregation](https://kubernetes.io/docs/reference/access-authn-authz/rbac/#aggregated-clusterroles).
The [kube-state-metrics chart](https://github.com/prometheus-community/helm-charts/blob/main/charts/kube-state-metrics/values.yaml)
also supports extra resource rules, namespace-scoped roles and aggregation.

Platforms can deploy exporters with disjoint namespace scopes. More replicas
watch the same scope rather than divide it. Karta does not manage that deployment.
