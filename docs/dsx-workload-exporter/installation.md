<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Install the DSX workload exporter with Helm

The exporter is part of the Karta Helm chart and runs without the Karta operator.
Helm and kubectl must point to the cluster where you want to install it.

## Install

Set `VERSION` to a published chart version that includes the exporter. Chart
version `0.3.0` predates it. The chart supplies the matching exporter image;
no values file is required.

```sh
VERSION=CHART_VERSION

helm upgrade --install dsx-workload \
  oci://ghcr.io/dsx-ai-factory/workload-map/karta \
  --version "$VERSION" \
  --namespace karta-system --create-namespace \
  --set operator.enabled=false \
  --set webhook.enabled=false \
  --set exporter.enabled=true \
  --wait --timeout 5m
```

Helm creates the installation namespace. The exporter observes all namespaces
except `kube-system`, `kube-public` and `kube-node-lease` by default.
Both bundled and cluster Karta definitions are enabled. The chart installs the
Karta CRD and maintains it through its upgrade hook. Built-in workload kinds do
not require creating Karta objects. Cluster definitions override bundled ones.

Use a separate namespace and release if Karta is already installed. To leave
its shared CRD unchanged, add both `--skip-crds` and
`--set crdUpgrader.enabled=false`.

## Verify

```sh
kubectl -n karta-system port-forward service/dsx-workload-exporter 8080:8080
```

In another terminal:

```sh
curl -fv http://127.0.0.1:8080/readyz
curl -fsS http://127.0.0.1:8080/metrics | grep '^dsx_'
```

Readiness returns HTTP 200 with an empty body when the definition inventory and
required watches are healthy. Inspect `dsx_exporter_observation_valid` for
collection and workload errors; a successful scrape alone does not establish valid observations.
See the [metric reference](../keps/0001-metrics-exporter/README.md#metrics).

## Configure

The [configuration reference](configuration.md#helm-values) covers namespace
selection, definition sources, labels, worker counts, resources and permissions.
To let an existing Prometheus Operator discover the exporter, follow
[Prometheus integration](configuration.md#prometheus-operator).

For a catalog-only installation without the Karta CRD, add these flags to the
install command:

```sh
--skip-crds \
--set crdUpgrader.enabled=false \
--set exporter.kartaDefinitionSource.cluster.enabled=false
```

For an unreleased checkout, use `./charts/karta` instead of the OCI URL and omit
`--version`. Set `exporter.image.repository` and `exporter.image.tag` to an image
built from that checkout. See [development](../../dsx-workload-exporter/README.md#development).

## Uninstall

```sh
helm uninstall dsx-workload --namespace karta-system
```

The namespace and shared Karta CRD remain. Other installations may use them.
