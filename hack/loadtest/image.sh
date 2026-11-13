#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Existing mode: build the exporter image for the node architecture and import it into
# containerd on every real node through kubectl. Nothing is pushed to a registry.
# For each node: a short-lived privileged Pod gets the tarball by kubectl cp, then
# imports it into the node's containerd with nsenter and ctr. The cluster must allow
# privileged, host-PID Pods; use this on a disposable test cluster only.
set -euo pipefail
# shellcheck source=hack/loadtest/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

[ "${CLUSTER_MODE}" = "existing" ] || die "image.sh is for CLUSTER_MODE=existing; kind mode loads the image in up.sh"

arch="${EXPORTER_IMAGE_PLATFORM#*/}"
tarball="${LOADTEST_DIR}/${RESULTS_DIR}/dsx-workload-exporter-${arch}.tar.gz"
host_dir="/tmp/loadtest-image"
mkdir -p "$(dirname "${tarball}")"

log "building ${EXPORTER_IMAGE} for ${EXPORTER_IMAGE_PLATFORM}"
GOARCH="${arch}" make -C "${REPO_ROOT}" exporter-image BUILD_ARGS="--platform ${EXPORTER_IMAGE_PLATFORM}" \
  EXPORTER_IMAGE_REPOSITORY="${EXPORTER_IMAGE%:*}" IMAGE_TAG="${EXPORTER_IMAGE##*:}" >/dev/null
docker save "${EXPORTER_IMAGE}" | gzip >"${tarball}"
log "saved ${tarball} ($(du -h "${tarball}" | cut -f1))"

kubectl create namespace "${RUNNER_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
# KWOK nodes carry type=kwok; every other node runs a real containerd.
read -r -a nodes <<<"$(kubectl get nodes -l 'type!=kwok' -o jsonpath='{.items[*].metadata.name}')"
[ "${#nodes[@]}" -gt 0 ] || die "no real nodes found"
cleanup() { kubectl -n "${RUNNER_NAMESPACE}" delete pod -l app=loadtest-image-loader --wait=false >/dev/null 2>&1 || true; }
trap cleanup EXIT

for i in "${!nodes[@]}"; do
  kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: image-loader-${i}
  namespace: ${RUNNER_NAMESPACE}
  labels: {app: loadtest-image-loader}
spec:
  nodeName: ${nodes[$i]}
  hostPID: true
  restartPolicy: Never
  tolerations: [{operator: Exists}]
  containers:
    - name: loader
      image: ${IMAGE_LOADER_IMAGE}
      command: [sleep, "3600"]
      securityContext: {privileged: true}
      volumeMounts: [{name: host, mountPath: /host}]
  volumes: [{name: host, hostPath: {path: ${host_dir}, type: DirectoryOrCreate}}]
EOF
done
kubectl -n "${RUNNER_NAMESPACE}" wait pod -l app=loadtest-image-loader --for=condition=Ready --timeout=5m >/dev/null

for i in "${!nodes[@]}"; do
  log "importing into ${nodes[$i]}"
  kubectl -n "${RUNNER_NAMESPACE}" cp "${tarball}" "image-loader-${i}:/host/exporter.tar.gz"
  kubectl -n "${RUNNER_NAMESPACE}" exec "image-loader-${i}" -- sh -c "
    gunzip -f /host/exporter.tar.gz &&
    nsenter -t 1 -m -- ctr -n k8s.io images import ${host_dir}/exporter.tar >/dev/null &&
    rm -f /host/exporter.tar"
done
log "${EXPORTER_IMAGE} is on ${#nodes[@]} nodes. Next: make loadtest-up CLUSTER_MODE=existing TIER=<tier>"
