<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Install the DSX workload exporter with Helm

The exporter is part of the Karta Helm chart. This example uses bundled
definitions without installing the Karta operator, CRDs or admission policies.
Choose a fresh namespace and release so an existing installation is not changed.

Helm and kubectl must be configured for your cluster.

## 1. Configure

See the [configuration reference](configuration.md#helm-values) for all options,
including images, namespace selection, Karta definitions, labels, workers and
resources.

Save this as `dsx-demo-values.yaml`. Set the image repository and tag to an
exporter image your cluster can pull. For a release with a matching published
exporter image, omit `exporter.image` to use the chart defaults:

```yaml
operator:
  enabled: false
crdUpgrader:
  enabled: false
exporter:
  enabled: true
  image:
    repository: registry.example.com/team/dsx-workload-exporter
    tag: YOUR_IMAGE_TAG
  kartaDefinitionSource:
    catalog:
      enabled: true
      include: [apps-deployment-v1]
    cluster:
      enabled: false
  namespaceSelector:
    matchLabels:
      kubernetes.io/metadata.name: dsx-demo
    matchExpressions: []
  rbac:
    workloadRules:
      - apiGroups: [apps]
        resources: [deployments, replicasets]
```

This scopes observation to `dsx-demo`. The explicit read rules cover Deployment
roots and their ReplicaSet owners; Pod and Namespace reads are supplied by the
chart. Runtime scope does not make these grants namespace-scoped.

## 2. Install or upgrade

Use a published chart version that includes the DSX workload exporter. Replace
`CHART_VERSION` with that version:

```sh
helm upgrade --install dsx-demo oci://ghcr.io/dsx-ai-factory/workload-map/karta \
  --version CHART_VERSION \
  --namespace dsx-demo --create-namespace --skip-crds \
  -f dsx-demo-values.yaml \
  --wait --timeout 5m
```

For an unreleased checkout, run the same command from the repository root with
`./charts/karta` instead of the OCI URL and omit `--version`. Set
`exporter.image` to an image built from that checkout.

```sh
kubectl -n dsx-demo rollout status deployment/dsx-workload-exporter --timeout=120s
```

`--skip-crds` prevents Helm from installing CRDs. Disabling `crdUpgrader` also
prevents the chart's CRD upgrade hook. Disabling the cluster definition source
removes the exporter's need for installed Karta definitions.

## 3. Check metrics

Create a small workload using an image available to your cluster:

```sh
kubectl -n dsx-demo create deployment demo --image=registry.k8s.io/pause:3.10
kubectl -n dsx-demo rollout status deployment/demo --timeout=120s
kubectl -n dsx-demo port-forward service/dsx-workload-exporter 8080:8080
```

In another terminal:

```sh
curl -fsS http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/metrics | grep '^dsx_'
```

Expect Pod attribution and status rows for `workload_name="demo"`, plus its
creation timestamp. Inspect `observation_valid` and diagnostic rows rather than
using HTTP success alone. See [metric semantics](../keps/0001-metrics-exporter/README.md#metrics).

## Cleanup

Only remove the demo resources created above:

```sh
kubectl -n dsx-demo delete deployment demo
helm uninstall dsx-demo --namespace dsx-demo
kubectl delete namespace dsx-demo
```
