#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
ARTIFACTS="$ROOT/.artifacts/exporter-e2e/${EXPORTER_E2E_CLUSTER:-dsx-exporter-e2e}"
export KUBECONFIG="$ARTIFACTS/kubeconfig"
mkdir -p "$ARTIFACTS"
[[ "$(kubectl config current-context)" == "kind-${EXPORTER_E2E_CLUSTER:-dsx-exporter-e2e}" ]] || {
  echo "expected the isolated exporter Kind context" >&2
  exit 1
}
collect() {
  local rc=$?
  for shard in a b; do
    kubectl -n "exporter-e2e-$shard" logs deploy/dsx-workload-exporter --all-containers \
      >"$ARTIFACTS/exporter-$shard.log" 2>&1 || true
  done
  kubectl get events -A --sort-by=.lastTimestamp >"$ARTIFACTS/events.txt" 2>&1 || true
  if (( rc != 0 )); then
    kubectl get pods,deployments,jobs -A -o yaml >"$ARTIFACTS/workloads.yaml" 2>&1 || true
    kubectl get kartas -o yaml >"$ARTIFACTS/kartas.yaml" 2>&1 || true
    for shard in a b; do
      kubectl get --raw "/api/v1/namespaces/exporter-e2e-$shard/services/dsx-workload-exporter:8080/proxy/metrics" \
        >"$ARTIFACTS/metrics-$shard.txt" 2>&1 || true
    done
  fi
  return "$rc"
}
trap collect EXIT
cd "$ROOT/dsx-workload-exporter"
go test -tags e2e -count=1 -v -timeout 15m ./test/e2e/... 2>&1 | tee "$ARTIFACTS/tests.log"
