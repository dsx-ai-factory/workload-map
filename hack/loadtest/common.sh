#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Shared setup for the scale test scripts: configuration, the cluster kubeconfig and
# small helpers. Sourced, never run.
# shellcheck disable=SC2034  # variables are used by the scripts that source this file

LOADTEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${LOADTEST_DIR}/../.." && pwd)"

# loadtest.env first: global.env also defaults CLUSTER_NAME, and the first one wins.
# shellcheck source=hack/loadtest/loadtest.env
source "${LOADTEST_DIR}/loadtest.env"
# shellcheck source=hack/loadtest/tiers.env
source "${LOADTEST_DIR}/tiers.env"
# KIND_NODE_IMAGE, JOBSET_VERSION and LWS_VERSION come from the e2e pins.
# shellcheck source=hack/e2e/global.env
source "${REPO_ROOT}/hack/e2e/global.env"

# A kubeconfig of its own, so a context switch in another terminal cannot redirect a run.
case "${CLUSTER_MODE}" in
  kind)
    KUBECONFIG="${KUBECONFIG_LOADTEST:-${HOME}/.kube/kind-${CLUSTER_NAME}.kubeconfig}" ;;
  existing)
    LOADTEST_CONTEXT="${LOADTEST_CONTEXT:-$(kubectl config current-context)}"
    _snapshot="${HOME}/.kube/loadtest-${LOADTEST_CONTEXT}.kubeconfig"
    # Via a temporary file: a nested script (report.sh from run.sh) already reads
    # KUBECONFIG from the snapshot, and a direct redirect would truncate it first.
    (umask 077 && kubectl config view --minify --flatten --context "${LOADTEST_CONTEXT}" >"${_snapshot}.tmp" &&
      mv "${_snapshot}.tmp" "${_snapshot}")
    KUBECONFIG="${_snapshot}"
    unset _snapshot ;;
  *) echo "unknown CLUSTER_MODE ${CLUSTER_MODE}: use kind or existing" >&2; exit 1 ;;
esac
export KUBECONFIG CLUSTER_NAME

log() { printf '%s  %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

# wait_until <description> <timeout seconds> <command...>: polls every 15s.
wait_until() {
  local what="$1" timeout="$2" deadline
  shift 2
  deadline=$(($(date +%s) + timeout))
  until "$@"; do
    [ "$(date +%s)" -lt "${deadline}" ] || die "timed out after ${timeout}s waiting for ${what}"
    sleep 15
  done
}

# retry <command...>: up to 5 attempts, 10s apart. One failed call to a flaky API
# server (a TLS handshake timeout) must not end a run that took half an hour to reach.
retry() {
  local attempt
  for attempt in 1 2 3 4 5; do
    "$@" && return 0
    [ "${attempt}" -lt 5 ] || return 1
    log "  call failed, retry ${attempt}/4 in 10s: $1 $2 $3"
    sleep 10
  done
}

# prom <query> [unix time]: prints the first value of an instant query, or 0.
prom() {
  local args=(--data-urlencode "query=$1")
  [ -n "${2:-}" ] && args+=(--data-urlencode "time=$2")
  curl -fsS "http://localhost:${PROMETHEUS_PORT}/api/v1/query" "${args[@]}" |
    jq -r '.data.result[0].value[1] // "0"'
}

# The Services of the monitoring release; the Grafana Secret has the Service name.
prometheus_service() {
  kubectl -n "${MONITORING_NAMESPACE}" get svc -l "app=kube-prometheus-stack-prometheus,release=${MONITORING_RELEASE}" \
    -o jsonpath='{.items[0].metadata.name}'
}
grafana_service() {
  kubectl -n "${MONITORING_NAMESPACE}" get svc -l "app.kubernetes.io/name=grafana,app.kubernetes.io/instance=${MONITORING_RELEASE}" \
    -o jsonpath='{.items[0].metadata.name}'
}

# forward <service> <local:remote>: a port-forward that reconnects; one dropped
# connection (a sleeping laptop, an API server restart) ends kubectl port-forward.
forward() {
  while :; do
    kubectl -n "${MONITORING_NAMESPACE}" port-forward "svc/$1" "$2" >/dev/null 2>&1 || true
    sleep 2
  done
}

# stop_forwards: ends each reconnect loop and its kubectl child.
stop_forwards() {
  local pid
  # The expansion guard: macOS bash 3.2 treats an empty array as unset under set -u.
  for pid in ${PF_PIDS[@]+"${PF_PIDS[@]}"}; do
    pkill -P "${pid}" 2>/dev/null || true
    kill "${pid}" 2>/dev/null || true
  done
}

# port_forwards: Prometheus and Grafana on localhost until the calling script exits.
# A port that already answers (another run, make loadtest-grafana) is left as is.
port_forwards() {
  PF_PIDS=()
  if ! curl -fsS -o /dev/null "http://localhost:${PROMETHEUS_PORT}/-/ready" 2>/dev/null; then
    forward "$(retry prometheus_service)" "${PROMETHEUS_PORT}:9090" &
    PF_PIDS+=("$!")
  fi
  if [ "${GRAFANA_ENABLED}" = "true" ] && ! curl -fsS -o /dev/null "http://localhost:${GRAFANA_PORT}/api/health" 2>/dev/null; then
    forward "$(retry grafana_service)" "${GRAFANA_PORT}:80" &
    PF_PIDS+=("$!")
  fi
  trap stop_forwards EXIT
  # Long enough for several reconnects when connections to the API server hang.
  wait_until "the Prometheus port-forward" 300 curl -fsS -o /dev/null "http://localhost:${PROMETHEUS_PORT}/-/ready"
}

# grafana_mark <text>: a phase marker on every panel of the dashboard.
grafana_mark() {
  [ "${GRAFANA_ENABLED}" = "true" ] || return 0
  # A marker is best effort: a failed lookup must not end the run.
  local service password
  service="$(grafana_service 2>/dev/null)" || service=""
  [ -n "${service}" ] || { log "grafana annotation skipped: Service not found"; return 0; }
  password="$(kubectl -n "${MONITORING_NAMESPACE}" get secret "${service}" \
    -o jsonpath='{.data.admin-password}' 2>/dev/null | base64 -d)" ||
    { log "grafana annotation skipped: no admin password"; return 0; }
  curl -fsS -u "admin:${password}" -H 'Content-Type: application/json' \
    -X POST "http://localhost:${GRAFANA_PORT}/api/annotations" \
    -d "{\"tags\":[\"karta-load\",\"$1\"],\"text\":\"$1 - tier ${TIER}\"}" >/dev/null ||
    log "grafana annotation failed (ignored)"
}
