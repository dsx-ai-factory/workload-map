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
arch="$(docker version --format '{{.Server.Arch}}')"
platform="linux/$arch"
GOARCH="$arch" make -C "$ROOT" exporter-image \
  CONTAINER_TOOL=docker EXPORTER_IMAGE_REPOSITORY=dsx-workload-exporter IMAGE_TAG=e2e \
  BUILD_ARGS="--platform $platform"
kind load docker-image dsx-workload-exporter:e2e --name "$CLUSTER_NAME"
# Export one platform: Docker's containerd store can retain an incomplete index.
# The test fixtures pin the same tags.
for image in busybox:1.37.0 otel/opentelemetry-collector:0.166.0; do
  archive="$ARTIFACTS/$(basename "${image%%:*}").tar"
  docker image inspect --platform "$platform" "$image" >/dev/null 2>&1 ||
    docker pull --platform "$platform" "$image"
  docker image save --platform "$platform" "$image" -o "$archive"
  kind load image-archive "$archive" --name "$CLUSTER_NAME"
done
# Reuse the recorder's pinned installers, without its full operator stack.
for operator in jobset lws; do
  bash "$ROOT/hack/e2e/operators/$operator/install.sh"
done
for shard in a b; do
  bash "$ROOT/hack/e2e/dsx-workload-exporter/install.sh" \
    "exporter-e2e-$shard" "exporter-e2e-$shard" \
    "$ROOT/hack/e2e/dsx-workload-exporter/values.yaml" \
    --set "exporter.selection.namespaceSelector.matchLabels.exporter-e2e-shard=$shard"
done
