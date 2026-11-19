#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Provision the scale test cluster: kind with raised control-plane limits, the KWOK
# controller and SCALE_NODES fake nodes, kube-prometheus-stack with Grafana, the JobSet
# and LWS operators, and the exporter built from this checkout.
# Usage: make loadtest-up TIER=10k|50k|150k|custom. See hack/loadtest/README.md.
set -euo pipefail
# shellcheck source=hack/loadtest/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

create_cluster() {
  if [ "${CLUSTER_MODE}" = "existing" ]; then
    log "existing cluster: context ${LOADTEST_CONTEXT}"
    kubectl get --raw /readyz >/dev/null || die "the API server of ${LOADTEST_CONTEXT} is not ready"
    return
  fi
  if kind get clusters | grep -qx "${CLUSTER_NAME}"; then
    log "kind cluster ${CLUSTER_NAME} exists; reusing it"
    kind export kubeconfig --name "${CLUSTER_NAME}" --kubeconfig "${KUBECONFIG}"
    return
  fi
  mkdir -p "$(dirname "${KUBECONFIG}")"
  kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}" \
    --config "${LOADTEST_DIR}/kind-config.yaml" --kubeconfig "${KUBECONFIG}" --wait 5m
}

install_kwok() {
  local base="https://github.com/kubernetes-sigs/kwok/releases/download/${KWOK_VERSION}"
  # "name|ours": empty when absent. A failed read must not look like "absent", or a
  # slow API server would make this install KWOK over the cluster's own release.
  local existing
  existing="$(kubectl -n kube-system get deploy/kwok-controller --ignore-not-found \
    -o jsonpath='{.metadata.name}|{.metadata.labels.loadtest\.karta/installed}')" ||
    die "cannot read kube-system/kwok-controller; retry when the API server answers"
  if [ -n "${existing}" ] && [ "${existing#*|}" != "true" ]; then
    # A KWOK the cluster already runs (for example a Helm release) is reused as is;
    # only the stages change: Job Pods must stay Running, and churn needs the fail stage.
    log "reusing the KWOK controller already in the cluster ($(kubectl -n kube-system get deploy/kwok-controller \
      -o jsonpath='{.spec.template.spec.containers[0].image}'))"
    kubectl delete stage pod-complete --ignore-not-found >/dev/null
    kubectl apply --server-side -f "${LOADTEST_DIR}/kwok/stages.yaml" >/dev/null
    return
  fi
  log "KWOK ${KWOK_VERSION}"
  kubectl apply --server-side -f "${base}/kwok.yaml" >/dev/null
  kubectl -n kube-system label deploy/kwok-controller loadtest.karta/installed=true --overwrite >/dev/null
  # Job Pods must stay Running, so pod-complete never gets applied: applying it and
  # deleting it afterwards completes every Job Pod in between on a rerun.
  curl -fsSL "${base}/stage-fast.yaml" |
    perl -0777 -ne 'print join("---\n", grep { !/^  name: pod-complete$/m } split(/^---\n/m))' |
    kubectl apply --server-side -f - >/dev/null
  kubectl delete stage pod-complete --ignore-not-found >/dev/null
  kubectl apply --server-side -f "${LOADTEST_DIR}/kwok/stages.yaml" >/dev/null
  kubectl -n kube-system rollout status deploy/kwok-controller --timeout=5m >/dev/null
}

create_nodes() {
  local capacity=$((SCALE_NODES * SCALE_PODS_PER_NODE)) i manifest
  [ "${capacity}" -ge "${SCALE_PODS}" ] ||
    die "${SCALE_NODES} nodes x ${SCALE_PODS_PER_NODE} Pods hold ${capacity}, less than ${SCALE_PODS}"
  log "${SCALE_NODES} KWOK nodes, ${SCALE_PODS_PER_NODE} Pods each"
  manifest="$(mktemp)"
  for ((i = 0; i < SCALE_NODES; i++)); do
    cat <<EOF
---
apiVersion: v1
kind: Node
metadata:
  name: kwok-node-${i}
  annotations:
    node.alpha.kubernetes.io/ttl: "0"
    kwok.x-k8s.io/node: fake
  # No kubernetes.io/os label: DaemonSets that select linux and tolerate every taint
  # (kindnet, kube-proxy, node-exporter) would otherwise add a fake Pod on each node.
  labels:
    kubernetes.io/arch: amd64
    kubernetes.io/hostname: kwok-node-${i}
    kubernetes.io/role: agent
    node-role.kubernetes.io/agent: ""
    type: kwok
    loadtest.karta/node: "true"
spec:
  taints:
    - key: kwok.x-k8s.io/node
      value: fake
      effect: NoSchedule
status:
  allocatable: {cpu: "64", memory: 512Gi, pods: "${SCALE_PODS_PER_NODE}"}
  capacity: {cpu: "64", memory: 512Gi, pods: "${SCALE_PODS_PER_NODE}"}
EOF
  done >"${manifest}"
  # The scale test owns these nodes; kube-controller-manager also writes their ttl
  # annotation, which conflicts on a rerun without --force-conflicts.
  kubectl apply --server-side --force-conflicts -f "${manifest}" >/dev/null
  rm -f "${manifest}"
}

install_monitoring() {
  local version="${KUBE_PROMETHEUS_STACK_VERSION}" operator=true existing
  # Another release's operator already reconciles Prometheus CRs cluster wide: reuse it
  # and its CRDs, at its chart version, and add only Prometheus, Grafana and kube-state-metrics.
  existing="$(helm list -A -o json | jq -r --arg own "${MONITORING_RELEASE}" \
    '[.[] | select(.chart | startswith("kube-prometheus-stack-")) | select(.name != $own)][0].chart // empty')"
  if [ -n "${existing}" ]; then
    version="${existing#kube-prometheus-stack-}"
    operator=false
    log "reusing the Prometheus operator of ${existing}"
  fi
  log "kube-prometheus-stack ${version}: release ${MONITORING_RELEASE} in ${MONITORING_NAMESPACE}"
  helm upgrade -i "${MONITORING_RELEASE}" oci://ghcr.io/prometheus-community/charts/kube-prometheus-stack \
    --version "${version}" -n "${MONITORING_NAMESPACE}" --create-namespace \
    -f "${LOADTEST_DIR}/monitoring/values.yaml" --set "grafana.enabled=${GRAFANA_ENABLED}" \
    --set "prometheusOperator.enabled=${operator}" --set "crds.enabled=${operator}" \
    --wait --timeout 15m >/dev/null
}

install_operators() {
  # The e2e installers pin the versions and wait for each controller. A rerun skips an
  # installed operator: its server-side apply would conflict with the LWS scale below.
  kubectl -n jobset-system get deploy/jobset-controller-manager >/dev/null 2>&1 ||
    "${REPO_ROOT}/hack/e2e/operators/jobset/install.sh"
  if ! kubectl -n lws-system get deploy/lws-controller-manager >/dev/null 2>&1; then
    "${REPO_ROOT}/hack/e2e/operators/lws/install.sh"
    # LWS ships 2 replicas of 1 CPU each; one is enough and frees room on the single worker.
    kubectl -n lws-system scale deploy/lws-controller-manager --replicas=1 >/dev/null
  fi
}

install_exporter() {
  local go_mem_limit="${EXPORTER_GO_MEM_LIMIT}"
  if [ "${go_mem_limit}" = "budget" ]; then
    # shellcheck source=hack/loadtest/budgets.env
    source "${LOADTEST_DIR}/budgets.env"
    # The chart takes a number of GiB: 90% of the budget, to the MiB.
    go_mem_limit="$(awk -v b="${BUDGET_MEMORY}" 'BEGIN { printf "%.3f", b * 0.9 / 1073741824 }')"
  fi
  if [ "${CLUSTER_MODE}" = "kind" ]; then
    log "exporter image ${EXPORTER_IMAGE}"
    make -C "${REPO_ROOT}" exporter-image \
      EXPORTER_IMAGE_REPOSITORY="${EXPORTER_IMAGE%:*}" IMAGE_TAG="${EXPORTER_IMAGE##*:}" >/dev/null
    kind load docker-image "${EXPORTER_IMAGE}" --name "${CLUSTER_NAME}" >/dev/null
  else
    log "exporter image ${EXPORTER_IMAGE}: must already be on the nodes (make loadtest-image)"
  fi
  log "exporter: workers ${EXPORTER_WORKERS}, apiQPS ${EXPORTER_API_QPS}, apiBurst ${EXPORTER_API_BURST}, GOMEMLIMIT ${go_mem_limit}"
  helm upgrade -i karta "${REPO_ROOT}/charts/karta" -n "${EXPORTER_NAMESPACE}" --create-namespace \
    -f "${LOADTEST_DIR}/exporter-values.yaml" \
    --set "exporter.image.repository=${EXPORTER_IMAGE%:*}" --set "exporter.image.tag=${EXPORTER_IMAGE##*:}" \
    --set "exporter.workers=${EXPORTER_WORKERS}" \
    --set "exporter.apiQPS=${EXPORTER_API_QPS}" --set "exporter.apiBurst=${EXPORTER_API_BURST}" \
    --set "exporter.resources.limits.memory=${EXPORTER_MEMORY_LIMIT}" \
    --set-string "exporter.goMemLimit=${go_mem_limit}" \
    --set "exporter.pprof.enabled=${EXPORTER_PPROF}" >/dev/null
  # A config-only change keeps the Pod; a new image with the same tag needs a restart.
  kubectl -n "${EXPORTER_NAMESPACE}" rollout restart "deploy/${EXPORTER_NAME}" >/dev/null
  if ! kubectl -n "${EXPORTER_NAMESPACE}" rollout status "deploy/${EXPORTER_NAME}" --timeout=5m >/dev/null; then
    kubectl -n "${EXPORTER_NAMESPACE}" get pods -l "app.kubernetes.io/name=${EXPORTER_NAME}" \
      -o jsonpath='{range .items[*]}{.metadata.name} {.status.containerStatuses[0].state.waiting.reason}{"\n"}{end}' |
      grep -q ErrImageNeverPull && die "${EXPORTER_IMAGE} is not on the nodes; run make loadtest-image and follow it"
    die "the exporter did not become ready; see kubectl -n ${EXPORTER_NAMESPACE} describe deploy/${EXPORTER_NAME}"
  fi
  sed -e "s/SCRAPE_INTERVAL/${EXPORTER_SCRAPE_INTERVAL}/" -e "s/SCRAPE_TIMEOUT/${EXPORTER_SCRAPE_TIMEOUT}/" \
    -e "s/MONITORING_RELEASE/${MONITORING_RELEASE}/" \
    "${LOADTEST_DIR}/monitoring/servicemonitor.yaml" | kubectl apply -f - >/dev/null
}

install_dashboard() {
  [ "${GRAFANA_ENABLED}" = "true" ] || return 0
  kubectl -n "${MONITORING_NAMESPACE}" create configmap karta-load-dashboard \
    --from-file=karta-load.json="${LOADTEST_DIR}/grafana/dashboard.json" --dry-run=client -o yaml |
    kubectl label --local -f - grafana_dashboard=1 -o yaml | kubectl apply -f - >/dev/null
}

main() {
  log "tier ${TIER}: ${SCALE_PODS} Pods on ${SCALE_NODES} KWOK nodes, mode ${CLUSTER_MODE}"
  [ "${TIER}" != "150k" ] || [ "${CLUSTER_MODE}" != "kind" ] ||
    log "WARNING: 150k needs a dedicated control plane; local kind is sized for 50k"
  create_cluster
  install_kwok
  create_nodes
  install_monitoring
  install_operators
  install_exporter
  install_dashboard
  log "ready. KUBECONFIG=${KUBECONFIG}"
  log "next: make loadtest-run TIER=${TIER}"
}

main "$@"
