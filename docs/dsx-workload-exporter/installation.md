<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Install the DSX workload exporter with Helm

This demo uses the local checkout's chart and bundled definitions. It does not
install the Karta operator, CRDs or admission policies. Choose a fresh namespace
and release so an existing exporter installation is not changed.

## 1. Choose an image

Run from the repository root with Helm and kubectl installed. Select the intended
kubectl context before installing:

```sh
kubectl config use-context YOUR_CONTEXT
```

For an unreleased branch, use an image built from this checkout. Do not assume
the chart appVersion has a published exporter image. Set the repository and tag
to a registry you can push to and your cluster can pull from:

```sh
export EXPORTER_IMAGE_REPOSITORY=registry.example.com/team/dsx-workload-exporter
export EXPORTER_IMAGE_TAG=dev
```

If you do not already have that image, authenticate to your registry and build
and push the committed checkout with Docker Buildx. The archive excludes
ignored and untracked local files. Both architectures allow an ARM laptop to build
for AMD64 cluster nodes:

```sh
git archive HEAD | docker buildx build --platform linux/amd64,linux/arm64 \
  -f dsx-workload-exporter/Dockerfile \
  --build-arg GO_VERSION="$(awk '/^go /{print $2; exit}' dsx-workload-exporter/go.mod)" \
  --tag "$EXPORTER_IMAGE_REPOSITORY:$EXPORTER_IMAGE_TAG" --push -
```

For a local cluster, loading an image instead is sufficient if its repository,
tag and architecture match the values and nodes. The published image must be
accessible to cluster nodes. For a private image, configure pull credentials
through the chart-wide `imagePullSecrets` setting.

## 2. Configure and install

Save this as `dsx-demo-values.yaml`:

```yaml
operator:
  enabled: false
crdUpgrader:
  enabled: false
exporter:
  enabled: true
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

```sh
: "${EXPORTER_IMAGE_REPOSITORY:?Set the image repository}"
: "${EXPORTER_IMAGE_TAG:?Set the image tag}"
helm upgrade --install dsx-demo ./charts/karta \
  --namespace dsx-demo --create-namespace --skip-crds \
  -f dsx-demo-values.yaml \
  --set-string exporter.image.repository="$EXPORTER_IMAGE_REPOSITORY" \
  --set-string exporter.image.tag="$EXPORTER_IMAGE_TAG" \
  --wait --timeout 5m
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
using HTTP success alone. See [metric semantics](metrics.md) and
[configuration](configuration.md) to add labels or change scope.

## Cleanup

Only remove the demo resources created above:

```sh
kubectl -n dsx-demo delete deployment demo
helm uninstall dsx-demo --namespace dsx-demo
kubectl delete namespace dsx-demo
```
