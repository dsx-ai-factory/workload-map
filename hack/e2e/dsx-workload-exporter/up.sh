#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
ARTIFACTS="$ROOT/.artifacts/exporter-e2e/${EXPORTER_E2E_CLUSTER:-dsx-exporter-e2e}"
export KUBECONFIG="$ARTIFACTS/kubeconfig"
# shellcheck source=/dev/null
source "$ROOT/hack/e2e/global.env"
CLUSTER_NAME="${EXPORTER_E2E_CLUSTER:-dsx-exporter-e2e}"
mkdir -p "$(dirname "$KUBECONFIG")"
if ! kind get clusters | grep -qx "$CLUSTER_NAME"; then
  kind create cluster --name "$CLUSTER_NAME" --image "$KIND_NODE_IMAGE" --kubeconfig "$KUBECONFIG" --wait 120s
else
  kind export kubeconfig --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG"
fi
GO_VERSION="$(awk '/^go /{print $2; exit}' "$ROOT/dsx-workload-exporter/go.mod")"
docker build --build-arg "GO_VERSION=$GO_VERSION" -f "$ROOT/dsx-workload-exporter/Dockerfile" -t dsx-workload-exporter:e2e "$ROOT"
kind load docker-image dsx-workload-exporter:e2e --name "$CLUSTER_NAME"
# Export one platform: Docker's containerd store can retain an incomplete index.
platform="linux/$(docker version --format '{{.Server.Arch}}')"
docker pull --platform "$platform" busybox:1.37.0
docker image save --platform "$platform" busybox:1.37.0 -o "$ARTIFACTS/busybox.tar"
kind load image-archive "$ARTIFACTS/busybox.tar" --name "$CLUSTER_NAME"
# Reuse the recorder's pinned installers, without its full operator stack.
for operator in jobset lws; do
  bash "$ROOT/hack/e2e/operators/$operator/install.sh"
done
for shard in a b; do
  installed=false
  if helm status "exporter-e2e-$shard" -n "exporter-e2e-$shard" >/dev/null 2>&1; then
    installed=true
  fi
  helm upgrade --install "exporter-e2e-$shard" "$ROOT/charts/karta" \
    --namespace "exporter-e2e-$shard" --create-namespace \
    -f "$ROOT/hack/e2e/dsx-workload-exporter/values.yaml" \
    --set "exporter.namespaceSelector.matchLabels.exporter-e2e-shard=$shard" \
    --wait --timeout 3m
  # A reused cluster must pick up the rebuilt image with the same local tag.
  if "$installed"; then
    kubectl -n "exporter-e2e-$shard" rollout restart deployment/dsx-workload-exporter
    kubectl -n "exporter-e2e-$shard" rollout status deployment/dsx-workload-exporter --timeout=180s
  fi
done
