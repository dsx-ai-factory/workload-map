#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# kind mode: delete the scale test cluster and its kubeconfig.
# existing mode: remove only what up.sh and run.sh added; the cluster stays.
# Results stay on disk in both modes.
set -euo pipefail
# shellcheck source=hack/loadtest/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

if [ "${CLUSTER_MODE}" = "kind" ]; then
  kind delete cluster --name "${CLUSTER_NAME}" --kubeconfig "${KUBECONFIG}"
  rm -f "${KUBECONFIG}"
  exit 0
fi

log "removing the scale test from ${LOADTEST_CONTEXT}"
kubectl delete ns -l loadtest.karta/target=true --wait=false --ignore-not-found
kubectl delete ns "${RUNNER_NAMESPACE}" --wait=false --ignore-not-found
kubectl delete clusterrolebinding loadtest-kube-burner --ignore-not-found
helm uninstall karta -n "${EXPORTER_NAMESPACE}" --ignore-not-found
helm uninstall "${MONITORING_RELEASE}" -n "${MONITORING_NAMESPACE}" --ignore-not-found
kubectl delete ns "${MONITORING_NAMESPACE}" --wait=false --ignore-not-found
# Only the nodes and the KWOK install that up.sh created; a KWOK the cluster already
# ran, and its own fake nodes, stay.
kubectl delete nodes -l loadtest.karta/node=true --ignore-not-found
kubectl delete stage pod-fail-on-annotation --ignore-not-found
if [ -n "$(kubectl -n kube-system get deploy/kwok-controller --ignore-not-found -o jsonpath='{.metadata.labels.loadtest\.karta/installed}')" ]; then
  kubectl delete -f "https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}/kwok.yaml" --ignore-not-found
fi
log "kept: the JobSet and LWS operators (shared with e2e) and ${EXPORTER_NAMESPACE} namespace objects outside the release"
