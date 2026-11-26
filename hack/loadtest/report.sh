#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Print the budget table for one run and write report.txt and summary.json next to it.
# Exits 1 when a budget fails. Usage: report.sh [results dir] (default: the newest run).
set -euo pipefail
# shellcheck source=hack/loadtest/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

RESULTS="${1:-$(find "${LOADTEST_DIR}/${RESULTS_DIR}" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort | tail -n 1)}"
[ -n "${RESULTS}" ] && [ -f "${RESULTS}/phases.env" ] || die "no run results found; pass a results directory"
# shellcheck source=/dev/null
source "${RESULTS}/config.env"
# shellcheck source=/dev/null
source "${RESULTS}/phases.env"
# shellcheck source=hack/loadtest/budgets.env
source "${LOADTEST_DIR}/budgets.env"

curl -fsS -o /dev/null "http://localhost:${PROMETHEUS_PORT}/-/ready" 2>/dev/null || port_forwards

job="job=\"${EXPORTER_NAME}\""
container="namespace=\"${EXPORTER_NAMESPACE}\",container=\"${EXPORTER_NAME}\""
# A run that started at churn has no steady phase; its context lines cover churn instead.
steady_start="${PHASE_steady:-${PHASE_churn:?no steady or churn phase in ${RESULTS}/phases.env}}"
if [ -n "${PHASE_steady:-}" ]; then
  steady_end="${PHASE_churn:-${PHASE_recover:-${PHASE_report:-$(date +%s)}}}"
else
  steady_end="${PHASE_drain:-${PHASE_recover:-${PHASE_report:-$(date +%s)}}}"
fi
window=$((steady_end - steady_start))

FAILED=0
ROWS=()
# check <phase> <name> <value> <budget> <unit>: one table row; a missing value fails.
check() {
  local verdict=PASS
  if [ -z "$3" ] || ! awk -v v="$3" -v b="$4" 'BEGIN { exit !(v <= b) }'; then
    verdict=FAIL
    FAILED=$((FAILED + 1))
  fi
  ROWS+=("$(printf '%-8s %-22s %14s %14s  %s' "$1" "$2 ($5)" "${3:--}" "$4" "${verdict}")")
  printf '{"phase":"%s","budget":"%s","unit":"%s","value":%s,"limit":%s,"pass":%s}\n' \
    "$1" "$2" "$5" "${3:-null}" "$4" "$([ "${verdict}" = PASS ] && echo true || echo false)" >>"${RESULTS}/budgets.ndjson"
}
seconds() { [ -n "${2:-}" ] && [ -n "${1:-}" ] && echo $(($2 - $1)); }

: >"${RESULTS}/budgets.ndjson"
[ -z "${PHASE_sync:-}" ] || check sync "rows after Pods" "$(seconds "${PODS_CREATED:-}" "${ROWS_SYNCED:-}")" "${BUDGET_SYNC}" s
[ -z "${PHASE_steady:-}" ] || check steady "memory max" "$(prom "max_over_time(max(container_memory_working_set_bytes{${container}})[${window}s:15s])" "${steady_end}")" "${BUDGET_MEMORY}" bytes
[ -z "${PHASE_steady:-}" ] || check steady "scrape max" "$(prom "max_over_time(max(scrape_duration_seconds{${job}})[${window}s:15s])" "${steady_end}")" "${BUDGET_SCRAPE}" s
[ -z "${PHASE_steady:-}" ] || check steady "API requests" "$(prom "sum(rate(rest_client_requests_total{${job}}[${window}s]))" "${steady_end}")" "${BUDGET_API_RPS}" req/s
[ -z "${PHASE_drain:-}" ] || check drain "queues empty" "$(seconds "${PHASE_drain}" "${DRAINED:-}")" "${BUDGET_DRAIN}" s
[ -z "${PHASE_recover:-}" ] || check recover "rows after restart" "$(seconds "${PHASE_recover}" "${RECOVERED:-}")" "${BUDGET_RECOVER}" s

end="${PHASE_report:-$(date +%s)}"
run=$((end - ${PHASE_create:-steady_start}))
info() { printf '  %-34s %s\n' "$1" "$2"; }
# queue_p99 <histogram> <window seconds> <end>: "pods=<s> workloads=<s>", the p99 per
# exporter queue over the window. The histogram buckets are powers of ten, so the
# value is an interpolated estimate within its decade.
queue_p99() {
  curl -fsS "http://localhost:${PROMETHEUS_PORT}/api/v1/query" \
    --data-urlencode "query=histogram_quantile(0.99, sum by (le, name) (increase($1{${job},name=~\"exporter-(pods|workloads)\"}[$2s])))" \
    --data-urlencode "time=$3" |
    jq -r '[.data.result[] | "\(.metric.name | sub("exporter-"; ""))=\(.value[1] | if . == "NaN" then "idle" else (tonumber * 1000 | round / 1000 | tostring) end)"] | sort | join(" ")'
}
{
  echo "tier ${TIER}  workloads ${WORKLOADS}  pods ${EXPECTED_PODS}  churn cycles ${CHURN_CYCLES:-0}"
  printf '%-8s %-22s %14s %14s  %s\n' phase budget value limit result
  printf '%s\n' "${ROWS[@]}"
  echo
  echo "context (no budget)"
  info "create (kube-burner) s" "$(seconds "${PHASE_create:-}" "${PHASE_sync:-}")"
  info "create -> all Pods s" "$(seconds "${PHASE_sync:-}" "${PODS_CREATED:-}")"
  info "exporter memory max, whole run" "$(prom "max_over_time(max(container_memory_working_set_bytes{${container}})[${run}s:15s])" "${end}")"
  # The live heap after a forced GC, from the phase's heap profile: comparable across
  # runs, unlike heap in use, which also holds garbage the GC has not collected yet.
  for label in steady drained recovered; do
    var="LIVE_HEAP_${label}"
    [ -z "${!var:-}" ] || info "exporter live heap, ${label}" "${!var}"
  done
  info "exporter Go heap in use, steady" "$(prom "max_over_time(max(go_memstats_heap_inuse_bytes{${job}})[${window}s:15s])" "${steady_end}")"
  info "exporter CPU cores, steady mean" "$(prom "sum(rate(container_cpu_usage_seconds_total{${container}}[${window}s]))" "${steady_end}")"
  info "scrape samples, steady" "$(prom "max(scrape_samples_scraped{${job}})" "${steady_end}")"
  info "API requests/s, churn mean" "$([ -n "${PHASE_drain:-}" ] && prom "sum(rate(rest_client_requests_total{${job}}[$((PHASE_drain - PHASE_churn))s]))" "${PHASE_drain}")"
  info "client throttle p99 s, run" "$(prom "histogram_quantile(0.99, sum by (le) (increase(rest_client_rate_limiter_duration_seconds_bucket{${job}}[${run}s])))" "${end}")"
  # Queue wait is the exporter's own freshness delay: from enqueue to a worker taking
  # the item. Work duration is the processing time of one item.
  info "queue wait p99 s, steady" "$(queue_p99 workqueue_queue_duration_seconds_bucket "${window}" "${steady_end}")"
  info "work duration p99 s, steady" "$(queue_p99 workqueue_work_duration_seconds_bucket "${window}" "${steady_end}")"
  if [ -n "${PHASE_churn:-}" ] && [ -n "${PHASE_drain:-}" ]; then
    info "queue wait p99 s, churn" "$(queue_p99 workqueue_queue_duration_seconds_bucket "$((PHASE_drain - PHASE_churn))" "${PHASE_drain}")"
    info "work duration p99 s, churn" "$(queue_p99 workqueue_work_duration_seconds_bucket "$((PHASE_drain - PHASE_churn))" "${PHASE_drain}")"
  fi
  info "etcd db bytes, max" "$(prom "max_over_time(max(apiserver_storage_size_bytes)[${run}s:30s])" "${end}")"
  info "apiserver RSS bytes, max" "$(prom "max_over_time(max(process_resident_memory_bytes{job=\"apiserver\"})[${run}s:30s])" "${end}")"
  echo
  if [ "${FAILED}" -eq 0 ]; then echo "PASS  all budgets"; else echo "FAIL  ${FAILED} budget(s)"; fi
  echo "results: ${RESULTS}"
} | tee "${RESULTS}/report.txt"

jq -s --arg tier "${TIER}" --argjson failed "${FAILED}" '{tier: $tier, pass: ($failed == 0), budgets: .}' \
  "${RESULTS}/budgets.ndjson" >"${RESULTS}/summary.json"
[ "${FAILED}" -eq 0 ]
