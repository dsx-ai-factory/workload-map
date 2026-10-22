<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta metrics exporter configuration

Proposed configuration for [KEP-0001](../../keps/0001-metrics-exporter/README.md).
The exporter runs separately from the Karta controller. Helm configures its
definitions, observation scope, submitter attribution and metric delivery.

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
      exclude: [core-pod-v1] # Clear to opt into bare-Pod catalog attribution.
    cluster:
      enabled: true # Cluster-installed Karta definitions; default true.
      include: []
      exclude: []
      labelSelector: {} # Select cluster Karta definitions by their labels.
  # Watch Pods and workload objects only in matching namespaces.
  namespaceSelector: # Default null or {}: all namespaces.
    matchExpressions:
      - key: example.com/tenant # Include namespaces carrying this label.
        operator: Exists
      - key: kubernetes.io/metadata.name
        operator: NotIn
        values: [kube-system, karta-system] # Skip these system namespaces.
  # Match the workload root's metadata.labels; keep owner dependencies watched.
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
  # Read the submitter from workload and Pod metadata with CEL.
  attribution:
    submittedByExpression: | # Default "": attribution disabled.
      ("karta.run.ai/submitted-by" in annotations &&
       annotations["karta.run.ai/submitted-by"] != "")
        ? annotations["karta.run.ai/submitted-by"]
        : ("example.com/submitted-by" in labels
            ? labels["example.com/submitted-by"]
            : "")
    limits: {} # Optional evaluation bounds; see Submitter attribution.
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
| `kartaDefinitionSource.cluster.labelSelector` | Cluster Karta definitions by their labels. |
| Definition-source `include` / `exclude` | Definition names. Empty includes mean all; exclusions win. |

Use [Kubernetes label selectors](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors)
to match each object's own labels. All conditions must match; `NotIn` also matches
missing labels.

Namespace selection limits watches. All Pods and required owners in those
namespaces stay watched, including Jobs owned by a JobSet.

`objectSelector` selects workload roots. `podSelector` selects Pod rows; both must
match for a Pod row to appear. Workload status covers the full workload; joined
usage covers only included Pods. Excluding a root excludes its Pods too, without
treating its children as new roots.

Scope changes remove rows outside the selection. A failed refresh never expands
the scope.

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
none. Cluster definition names and their label selector must both match. Ties use
oldest creation time, then name.

The catalog excludes `core-pod-v1` by default. Clearing that exclusion, or selecting
a cluster Pod Karta, enables bare-Pod workloads. `objectSelector` also applies to
those roots.

`ignoredOwnerKinds` stops the owner walk before an exact GVK. The last verified
object below it becomes the root; missing owners below that boundary still fail.
A Karta definition on an intermediate owner does not by itself stop the walk.
An ignored owner can still be selected independently. `requiredKinds` checks
remain active; failed checks invalidate dependent observations.

## Submitter attribution

`attribution.submittedByExpression` is a CEL expression that returns the
submitter identity as a string. The exporter compiles it once at startup.
It runs on the root workload and its attributed Pods, as described in
[Who submitted the workload](../../keps/0001-metrics-exporter/README.md#who-submitted-the-workload).

The expression can read three maps of strings:

| Variable | Contents |
| --- | --- |
| `labels` | The object's labels. |
| `annotations` | The object's annotations. |
| `resource` | `namespace`, `name`, `group`, `version` and `kind` of the object. |

`resource.kind` lets one expression read different metadata per workload type.
Guard each key with `in`; reading a missing key is an evaluation error. An empty
result means unknown identity. An empty expression disables attribution.
A non-string result or invalid CEL rejects startup. CEL does not authenticate
identity, and the exporter makes no API reads and never changes the objects.

`attribution.limits` bounds each evaluation:

| Setting under `exporter.attribution.limits` | Default | Bounds |
| --- | --- | --- |
| `costLimit` | 10000 | CEL cost units one evaluation may spend. |
| `maxExpressionBytes` | 4096 | Length of `submittedByExpression`. |
| `maxIdentityBytes` | 1024 | Length of one extracted identity. |
| `interruptCheckFrequency` | 100 | Comprehension iterations between cancellation checks. |

A limit of 0 selects its default, except `interruptCheckFrequency`: 0 rejects
startup, because CEL then stops checking for cancellation. Negative limits
reject startup. An evaluation that exceeds a limit or is cancelled yields no
identity.

### How the exporter reads attribution settings

Helm renders `exporter.attribution` unchanged into a ConfigMap. The exporter
mounts it read-only and reads it with `--config`. The exporter needs no
ConfigMap RBAC. A configuration change rolls the exporter Pod, since the file is
read once at startup. Unknown keys and files with more than one YAML document
reject startup.

Each setting also has a command-line flag and an environment variable:

| Config file key | Flag | Environment variable |
| --- | --- | --- |
| (config file path) | `--config` | `KARTA_EXPORTER_CONFIG` |
| `attribution.submittedByExpression` | `--submitted-by-expression` | `KARTA_EXPORTER_SUBMITTED_BY_EXPRESSION` |
| `attribution.limits.costLimit` | `--submitter-cost-limit` | `KARTA_EXPORTER_SUBMITTER_COST_LIMIT` |
| `attribution.limits.maxExpressionBytes` | `--submitter-max-expression-bytes` | `KARTA_EXPORTER_SUBMITTER_MAX_EXPRESSION_BYTES` |
| `attribution.limits.maxIdentityBytes` | `--submitter-max-identity-bytes` | `KARTA_EXPORTER_SUBMITTER_MAX_IDENTITY_BYTES` |
| `attribution.limits.interruptCheckFrequency` | `--submitter-interrupt-check-frequency` | `KARTA_EXPORTER_SUBMITTER_INTERRUPT_CHECK_FREQUENCY` |

A flag wins over its environment variable, which wins over the config file,
which wins over the default. A flag or variable applies only when set to a
non-empty value, so an empty variable does not clear the file's setting. Flags or
environment variables alone are enough to run the exporter outside the cluster:

```sh
karta-metrics-exporter \
  --submitted-by-expression '"example.com/owner" in labels ? labels["example.com/owner"] : ""'
```

The chart passes only `--config`. `karta-metrics-exporter --help` lists every
flag with its default and environment variable.

## Submitter admission

`submitterAdmission` captures direct Kubernetes callers. Exporter CEL extraction
is configured separately, as described in [Submitter attribution](#submitter-attribution).

Capture uses `MutatingAdmissionPolicy` and `ValidatingAdmissionPolicy` with their
bindings. Both run in the API server without a Karta operator. MAP is
[stable in Kubernetes 1.36+](https://kubernetes.io/docs/reference/access-authn-authz/mutating-admission-policy/);
[1.34-1.35 requires beta feature/API enablement](https://v1-35.docs.kubernetes.io/docs/reference/access-authn-authz/mutating-admission-policy/).
VAP is [stable since 1.30](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/).
The chart rejects capture if required APIs are unavailable.

This example captures Deployments and Jobs in opted-in namespaces:

```yaml
submitterAdmission:
  enabled: true
  matchConstraints:
    namespaceSelector:
      matchLabels:
        karta.run.ai/capture-submitter: "true"
      matchExpressions:
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values: [kube-system, karta-system]
    resourceRules:
      - operations: ["CREATE"]
        apiGroups: ["apps"]
        apiVersions: ["v1"]
        resources: ["deployments"]
        scope: Namespaced
      - operations: ["CREATE"]
        apiGroups: ["batch"]
        apiVersions: ["v1"]
        resources: ["jobs"]
        scope: Namespaced
    excludeResourceRules: []
  matchConditions: [] # Optional CEL conditions on the admission request.
```

Capture follows [native Kubernetes matching](https://github.com/kubernetes/kubernetes/blob/v1.36.0/staging/src/k8s.io/api/admissionregistration/v1/types.go#L554-L642).
Rules select namespaced CREATE requests. Exclusions win and all conditions must
pass. An empty namespace selector matches all namespaces. Direct Pod capture uses
group `""`, version `v1`, resource `pods`. These settings do not filter exporter watches.

Mutation fills a missing `karta.run.ai/submitted-by` annotation. Within capture
scope, CREATE validation rejects a supplied value that differs from the caller.
Captured identity must stay protected from UPDATE overrides or removal, including
after capture scope narrows. The exact UPDATE matching rules are TBD in the
[implementation design](../design/metrics-exporter/README.md).
Administrators control namespace labels and policy settings. Policies use
`failurePolicy: Fail`; validation bindings use `validationActions: [Deny]`.

## Metric delivery

| Mode | Configuration | Requirements |
| --- | --- | --- |
| Prometheus pull | `metricsExporter: prometheus` | Prometheus scrapes `/metrics`. ServiceMonitor is optional. |
| Native OTLP push | `metricsExporter: otlp` | An OTLP receiver; endpoint and protocol under `otlp`. |
| Collector bridge | Prometheus mode, scraped by a Collector | The Collector forwards to the chosen backend. |

Combined native pull and push is deferred. CPU, memory and GPU measurements come
from other exporters; the [query guide](README.md) shows how to join them.

### Prometheus

![Karta provides workload identity; Prometheus joins it with resource measurements for dashboards and other consumers.](../../keps/0001-metrics-exporter/metrics-architecture.png)

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
and `watch`: read one owner, list existing objects and receive updates. API versions
are selected by `resourceRules`, not RBAC.

Include the root and every intermediate owner needed for attribution:

| Workload | Workload rules | Why |
| --- | --- | --- |
| Deployment | `apps`: `deployments`, `replicasets` | Pods belong to ReplicaSets, which belong to the Deployment. |
| JobSet | `jobset.x-k8s.io`: `jobsets`; `batch`: `jobs` | Jobs connect the Pods to the JobSet. |
| LeaderWorkerSet | `leaderworkerset.x-k8s.io`: `leaderworkersets`; `apps`: `statefulsets` | StatefulSets connect the Pods to the LeaderWorkerSet. |
| RayJob | `ray.io`: `rayjobs`, `rayclusters`; `batch`: `jobs` | Read the RayCluster and any submitter Job created by the RayJob. |

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
