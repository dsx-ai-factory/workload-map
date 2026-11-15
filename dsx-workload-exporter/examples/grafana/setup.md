<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Connect a real cluster

The dashboards join DSX workload identity to kubelet CPU/RAM and DCGM GPU
measurements. Inventory and structure need only DSX. Resource panels need the
corresponding collector. No recording rules, GPU activity observer or
kube-state-metrics are required.

Use an existing Prometheus and Grafana installation. The examples below use
Prometheus Operator with a ServiceMonitor selector of `release: prometheus`.
Replace that label with your installation's selector. Prometheus must also
select the namespaces containing the ServiceMonitors.

## Workload identity

[Install DSX](../../../docs/dsx-workload-exporter/installation.md) with these
additional Karta Helm values:

```yaml
exporter:
  serviceMonitor:
    enabled: true
    additionalLabels:
      release: prometheus
```

The chart's ServiceMonitor sets `honorLabels: true`, preserving the workload
`namespace`. A standalone Prometheus scrape needs `honor_labels: true`.
Discover individual exporter endpoints rather than scraping a load-balanced
Service address. The Prometheus resource must not set `overrideHonorLabels: true`.

Bundled and cluster definitions are enabled by default. Workloads still need a
selected Karta definition and must match the exporter's observation scope.
The chart excludes `kube-system`, `kube-public` and `kube-node-lease` by default.
For custom workload kinds, add a definition and read permissions for the root
and its intermediate owners. See [selection and permissions](../../../docs/dsx-workload-exporter/configuration.md#selection).

## CPU and RAM from kubelet

Scrape each node's kubelet `/metrics/cadvisor` endpoint. This is an endpoint
exposed by kubelet; there is no dashboard feature to enable inside cAdvisor and
no separate cAdvisor DaemonSet is needed. The collector needs network access,
valid kubelet TLS settings and authorization to read node metrics. Existing
kube-prometheus-stack installations supply kubelet discovery and scrape RBAC.
See [Kubernetes metrics](https://kubernetes.io/docs/concepts/cluster-administration/system-metrics/).

Merge these values into your kube-prometheus-stack configuration:

```yaml
kubelet:
  enabled: true
  serviceMonitor:
    enabled: true
    cAdvisor: true
    cAdvisorInterval: 30s
grafana:
  sidecar:
    datasources:
      httpMethod: POST
```

The [chart values](https://github.com/prometheus-community/helm-charts/blob/main/charts/kube-prometheus-stack/values.yaml)
define these settings. If installing monitoring only for these dashboards,
`defaultRules.create: false` and `kubeStateMetrics.enabled: false` are sufficient;
neither supplies a dashboard dependency.

Keep `container_cpu_usage_seconds_total` and
`container_memory_working_set_bytes`, including their `namespace`, `pod` and
`container` labels, through scraping and forwarding. A rule dropping
`container_.*` removes both metrics even when kubelet scrape health is good.
CPU needs several scrapes to calculate its five-minute rate.

For a dedicated cAdvisor scrape, this Prometheus rule keeps the two needed
metrics:

```yaml
metric_relabel_configs:
  - source_labels: [__name__]
    action: keep
    regex: 'container_cpu_usage_seconds_total|container_memory_working_set_bytes'
```

In a ServiceMonitor this is `metricRelabelings` with `sourceLabels`. Extend an
existing allowlist instead of adding a second `keep` rule: every keep rule must
match. Remove conflicting drop rules. A later keep cannot recover dropped
samples. Helm lists replace existing lists, so preserve other required rules
when changing `kubelet.serviceMonitor.cAdvisorMetricRelabelings`. Apply the same
check to remote-write filters and intermediate collectors.

## GPU measurements from DCGM

Use the DCGM Exporter already managed by your GPU installation, or install a
standalone exporter on nodes with NVIDIA drivers and a working NVIDIA container
runtime. Do not add a second exporter to nodes already running one.

The following `dcgm-values.yaml` example matches
[DCGM Exporter chart 4.8.3](https://github.com/NVIDIA/dcgm-exporter/blob/4.6.0-4.8.3/deployment/values.yaml):

```yaml
kubernetes:
  enablePodUID: true
  rbac:
    create: true
kubeletPath: /var/lib/kubelet/pod-resources
serviceMonitor:
  enabled: true
  honorLabels: true
  additionalLabels:
    release: prometheus
customMetrics: |
  DCGM_FI_DEV_GPU_UTIL, gauge, GPU activity in percent.
  DCGM_FI_DEV_FB_USED, gauge, Used framebuffer memory in MiB.
  DCGM_FI_PROF_GR_ENGINE_ACTIVE, gauge, Graphics engine active ratio.
```

`customMetrics` replaces the counter list. Add these entries to the existing
list if other dashboards need its counters. Profiling support and permissions
are required for the MIG activity counter. The dashboards expect GPU utilization
in percent and framebuffer memory in MiB. They do not consume
`DCGM_FI_DEV_GPU_UTIL_RATIO`; verify the metric names and units when changing
DCGM versions.

For a new standalone installation:

```sh
helm repo add gpu-helm-charts https://nvidia.github.io/dcgm-exporter/helm-charts
helm repo update gpu-helm-charts
helm upgrade --install dcgm-exporter gpu-helm-charts/dcgm-exporter \
  --version 4.8.3 --namespace monitoring --create-namespace \
  -f dcgm-values.yaml --wait --timeout 5m
```

This chart renders these container settings:

```yaml
env:
  - name: DCGM_EXPORTER_KUBERNETES
    value: "true"
  - name: DCGM_EXPORTER_KUBERNETES_ENABLE_POD_UID
    value: "true"
```

Kubernetes mapping is enabled by the chart; Pod UID mapping is opt-in.
The [DaemonSet](https://github.com/NVIDIA/dcgm-exporter/blob/4.6.0-4.8.3/deployment/templates/daemonset.yaml)
mounts the host's pod-resources directory at `/var/lib/kubelet/pod-resources`.
Its `kubelet.sock` must be accessible on each GPU node. Set `kubeletPath` if
the host uses a different kubelet root. The exporter also needs a mounted
ServiceAccount token and a ClusterRoleBinding granting `get`, `list` and `watch`
on Pods. The chart supplies these when UID mapping and its RBAC option are on.
For another deployment method, configuring the environment alone does not
supply the socket mount or Pod-read access.

Verify an allocated GPU Pod produces nonempty `pod_uid` and `UUID` labels.
MIG samples also need `GPU_I_ID`; retain `GPU_CI_ID` when present. The workload
namespace should be in `namespace` with this example. If your collector moves
it to `exported_namespace`, select that label in the dashboard. A successful
scrape without Pod UIDs cannot populate GPU attribution. DRA installations must
also emit Pod UIDs; ResourceClaim labels alone are insufficient.

## Grafana and verification

For an independently managed Grafana datasource, set its HTTP method to POST
in the UI or provision it with:

```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    url: http://prometheus.monitoring.svc:9090
    jsonData:
      httpMethod: POST
```

Replace the URL with the Prometheus query endpoint reachable from Grafana.
POST avoids proxy URL limits for the long activity queries. See
[Grafana provisioning](https://grafana.com/docs/grafana/latest/datasources/prometheus/configure/#provision-with-yaml).

Run each query separately in that datasource before importing:

```promql
count by (job) (dsx_workload_status)
count by (job) (dsx_workload_pod_info{observation_valid="true"})
count by (job) (container_cpu_usage_seconds_total{namespace!="",pod!="",container!=""})
count by (job) (container_memory_working_set_bytes{namespace!="",pod!="",container!=""})
count by (job) (DCGM_FI_DEV_GPU_UTIL{pod_uid!="",UUID!=""})
count by (job) (DCGM_FI_DEV_FB_USED{pod_uid!="",UUID!=""})
count by (job) (DCGM_FI_PROF_GR_ENGINE_ACTIVE{pod_uid!="",UUID!="",GPU_I_ID!=""})
```

The last query applies to MIG. Empty GPU results can be expected without
allocated GPU Pods. Empty Pod-info can be expected for a zero-Pod workload.
For a backend containing multiple clusters, add an exact `cluster` matcher to
each query and ensure every source carries that same label. For one unlabeled
cluster, leave the dashboard's Cluster field blank.

Return to [Set up and import](README.md#set-up-and-import) to import the JSON
files and select the discovered job names. Keep the dashboard UIDs so links
between Overview and Structure work.
