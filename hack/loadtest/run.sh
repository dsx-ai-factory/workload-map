#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Run one scale test against the cluster from up.sh, phase by phase:
#   create   a kube-burner Job in the cluster creates the workload roots
#   sync     wait until every load Pod has a dsx_workload_pod_info row
#   steady   nothing changes for PHASES_STEADY seconds
#   churn    replace PHASES_CHURN_PERCENT of the roots and fail Pods, each cycle
#   drain    wait until the exporter queues are empty and the rows match again
#   recover  restart the exporter and wait until the rows match again
# Phase times go to <results>/phases.env; report.sh turns them into the budget table.
set -euo pipefail
# shellcheck source=hack/loadtest/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

LOAD_NS_REGEX="${LOAD_NAMESPACE_PREFIX}-[0-9]+"

plan_counts() {
  local sum=$((MIX_DEPLOYMENT + MIX_JOB + MIX_JOBSET + MIX_LWS + MIX_POD))
  [ "${sum}" -eq 100 ] || die "the mix sums to ${sum}, not 100"
  [ $((SCALE_NODES * SCALE_PODS_PER_NODE)) -ge "${SCALE_PODS}" ] || die "the Pods do not fit on the nodes"
  per_ns() { echo $((SCALE_PODS * $1 / 100 / SCALE_NAMESPACES / $2)); }
  DEPLOYMENTS="$(per_ns "${MIX_DEPLOYMENT}" "${PODS_PER_WORKLOAD}")"
  JOBS="$(per_ns "${MIX_JOB}" "${PODS_PER_WORKLOAD}")"
  JOBSETS="$(per_ns "${MIX_JOBSET}" "${PODS_PER_WORKLOAD}")"
  LWS="$(per_ns "${MIX_LWS}" "${PODS_PER_WORKLOAD}")"
  BARE_PODS="$(per_ns "${MIX_POD}" 1)"
  WORKLOADS=$((SCALE_NAMESPACES * (DEPLOYMENTS + JOBS + JOBSETS + LWS + BARE_PODS)))
  EXPECTED_PODS=$((SCALE_NAMESPACES * ((DEPLOYMENTS + JOBS + JOBSETS + LWS) * PODS_PER_WORKLOAD + BARE_PODS)))
  local nodes
  # Not piped: under pipefail a failed kubectl ended the run without a message.
  nodes="$(retry kubectl get nodes -l type=kwok -o name)" || die "cannot list the KWOK nodes"
  nodes="$(printf '%s' "${nodes}" | grep -c . || true)"
  [ $((nodes * SCALE_PODS_PER_NODE)) -ge "${EXPECTED_PODS}" ] ||
    die "${nodes} KWOK nodes hold fewer than ${EXPECTED_PODS} Pods; run make loadtest-up TIER=${TIER}"
}

# phase <name>: records the phase start.
phase() {
  log "phase $1"
  echo "PHASE_${1}=$(date +%s)" >>"${RESULTS}/phases.env"
}

result() { echo "$1=$2" >>"${RESULTS}/phases.env"; }

# profiles <label>: saves heap, allocs and goroutine profiles of the running exporter
# under <results>/pprof, and records the live heap as LIVE_HEAP_<label>. A missing
# profile is logged, never fatal.
profiles() {
  [ "${EXPORTER_PPROF}" = "true" ] || return 0
  local dir="${RESULTS}/pprof" pf kind url live
  mkdir -p "${dir}"
  # Reconnects, like the Prometheus forward: one dropped connection lost whole phases.
  while :; do
    kubectl -n "${EXPORTER_NAMESPACE}" port-forward "deploy/${EXPORTER_NAME}" "${EXPORTER_PPROF_PORT}:6060" >/dev/null 2>&1 || true
    sleep 2
  done &
  pf=$!
  url="http://localhost:${EXPORTER_PPROF_PORT}/debug/pprof"
  if wait_for 60 curl -fsS -m 5 -o /dev/null "${url}/"; then
    for kind in heap allocs goroutine; do
      # gc=1 collects first, so the heap profile is the live heap, not the heap at
      # whatever collection ran last; with a high GOMEMLIMIT that can be minutes old.
      curl -fsS -m 120 -o "${dir}/$1-${kind}.pb.gz" "${url}/${kind}$([ "${kind}" = heap ] && echo '?gc=1')" ||
        log "  pprof ${kind} at $1 failed (ignored)"
    done
    log "  pprof: saved $1 profiles"
  else
    log "  pprof: the endpoint did not answer; $1 profiles skipped"
  fi
  pkill -P "${pf}" 2>/dev/null || true
  kill "${pf}" 2>/dev/null || true
  if [ -s "${dir}/$1-heap.pb.gz" ] && command -v go >/dev/null; then
    live="$(go tool pprof -sample_index=inuse_space -top -nodecount=1 "${dir}/$1-heap.pb.gz" 2>/dev/null |
      awk '/ total$/ { v = $(NF-1); sub(/total/, "", v); u = substr(v, length(v) - 1); n = v + 0
        m = (u == "kB") ? 1e3 : (u == "MB") ? 1e6 : (u == "GB") ? 1e9 : 1; printf "%.0f", n * m }')"
    [ -z "${live}" ] || result "LIVE_HEAP_$1" "${live}"
  fi
}

# cpu_profile <label> <seconds>: a CPU profile of the running exporter over <seconds>,
# saved as <results>/pprof/<label>-cpu.pb.gz. A failed capture is logged, never fatal.
# Its own port, so it can run in the background while profiles uses the other one.
cpu_profile() {
  [ "${EXPORTER_PPROF}" = "true" ] || return 0
  local dir="${RESULTS}/pprof" port=$((EXPORTER_PPROF_PORT + 1)) pf url
  mkdir -p "${dir}"
  while :; do
    kubectl -n "${EXPORTER_NAMESPACE}" port-forward "deploy/${EXPORTER_NAME}" "${port}:6060" >/dev/null 2>&1 || true
    sleep 2
  done &
  pf=$!
  url="http://localhost:${port}/debug/pprof"
  if wait_for 60 curl -fsS -m 5 -o /dev/null "${url}/"; then
    if curl -fsS -m $(($2 + 60)) -o "${dir}/$1-cpu.pb.gz" "${url}/profile?seconds=$2"; then
      log "  pprof: saved $1 CPU profile ($2s)"
    else
      log "  pprof: $1 CPU profile failed (ignored)"
    fi
  else
    log "  pprof: the endpoint did not answer; $1 CPU profile skipped"
  fi
  pkill -P "${pf}" 2>/dev/null || true
  kill "${pf}" 2>/dev/null || true
}

# wait_for <seconds> <command...>: polls every 2s; false on timeout, never fatal.
wait_for() {
  local deadline=$(($(date +%s) + $1))
  shift
  until "$@" 2>/dev/null; do
    [ "$(date +%s)" -lt "${deadline}" ] || return 1
    sleep 2
  done
}

# wait_until_time <unix time>: short sleeps against the wall clock. One long sleep
# stretches when the Mac sleeps, because macOS pauses sleep timers with the system.
wait_until_time() {
  while [ "$(date +%s)" -lt "$1" ]; do sleep 5; done
}

load_pods() { prom "count(kube_pod_info{namespace=~\"${LOAD_NS_REGEX}\"})"; }

pods_created() { [ "$(load_pods)" -ge "${EXPECTED_PODS}" ]; }

# rows_match [pod]: the exporter has a row for every load Pod that kube-state-metrics sees.
rows_match() {
  local selector="namespace=~\"${LOAD_NS_REGEX}\"" pods rows
  [ -n "${1:-}" ] && selector+=",pod=\"$1\""
  pods="$(load_pods)"
  rows="$(prom "count(dsx_workload_pod_info{${selector}})")"
  log "  load Pods ${pods}, exporter rows ${rows}"
  # Every existing load Pod has a row. The planned Pod count is the sync phase's
  # separate pods_created check: after churn, or when a run resumes, fewer Pods
  # than planned can exist, and that must not block the wait.
  [ "${pods}" -gt 0 ] && [ "${rows}" -ge "${pods}" ]
}

queues_empty() {
  [ "$(prom "sum(workqueue_depth{job=\"${EXPORTER_NAME}\"})")" = "0" ]
}

# exporter_pod: the newest exporter Pod.
exporter_pod() {
  retry kubectl -n "${EXPORTER_NAMESPACE}" get pods -l app.kubernetes.io/name="${EXPORTER_NAME}" \
    --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1:].metadata.name}'
}

# exporter_settled: the newest exporter Pod is scraped, its queues are empty and it
# has a row for every load Pod. Series of a Pod replaced moments ago (make loadtest-up
# restarts it) can still match, so every check names the current Pod.
exporter_settled() {
  local pod selector
  pod="$(exporter_pod)" || return 1
  selector="job=\"${EXPORTER_NAME}\",pod=\"${pod}\""
  [ "$(prom "count(workqueue_depth{${selector}})")" != "0" ] &&
    [ "$(prom "sum(workqueue_depth{${selector}})")" = "0" ] && rows_match "${pod}"
}

drained() { queues_empty && rows_match ""; }

run_create() {
  phase create
  # One rendered file, so each apply can be retried; a pipe cannot be read twice.
  local manifest="${RESULTS}/kube-burner.yaml"
  {
    kubectl create namespace "${RUNNER_NAMESPACE}" --dry-run=client -o yaml
    echo ---
    kubectl -n "${RUNNER_NAMESPACE}" create configmap kube-burner \
      --from-file="${LOADTEST_DIR}/kube-burner/config.yml" \
      --from-file="${LOADTEST_DIR}/kube-burner/templates" --dry-run=client -o yaml
    echo ---
    cat <<EOF
apiVersion: v1
kind: ServiceAccount
metadata: {name: kube-burner, namespace: ${RUNNER_NAMESPACE}}
---
# kube-burner creates namespaces and arbitrary workload kinds; local test cluster only.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: loadtest-kube-burner}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}
subjects: [{kind: ServiceAccount, name: kube-burner, namespace: ${RUNNER_NAMESPACE}}]
---
apiVersion: batch/v1
kind: Job
metadata: {name: kube-burner, namespace: ${RUNNER_NAMESPACE}}
spec:
  backoffLimit: 0
  template:
    spec:
      serviceAccountName: kube-burner
      restartPolicy: Never
      containers:
        - name: kube-burner
          image: ${KUBE_BURNER_IMAGE}
          args: [init, -c, config.yml, --log-level, info]
          env:
            - {name: NAMESPACES, value: "${SCALE_NAMESPACES}"}
            - {name: DEPLOYMENTS, value: "${DEPLOYMENTS}"}
            - {name: JOBS, value: "${JOBS}"}
            - {name: JOBSETS, value: "${JOBSETS}"}
            - {name: LWS, value: "${LWS}"}
            - {name: BARE_PODS, value: "${BARE_PODS}"}
            - {name: PODS_PER_WORKLOAD, value: "${PODS_PER_WORKLOAD}"}
            - {name: WORKLOAD_IMAGE, value: "${WORKLOAD_IMAGE}"}
            - {name: QPS, value: "${CREATE_QPS}"}
            - {name: BURST, value: "${CREATE_BURST}"}
          volumeMounts: [{name: config, mountPath: /config/templates-flat}]
          # The ConfigMap is flat; the config refers to templates/<file>.
          command: [sh, -c, 'mkdir -p /tmp/kb/templates && cp /config/templates-flat/config.yml /tmp/kb/ && cp /config/templates-flat/*.yml /tmp/kb/templates/ && cd /tmp/kb && exec kube-burner "\$@"', kube-burner]
      volumes: [{name: config, configMap: {name: kube-burner}}]
EOF
  } >"${manifest}"
  retry kubectl -n "${RUNNER_NAMESPACE}" delete job kube-burner --ignore-not-found --wait >/dev/null
  retry kubectl apply -f "${manifest}" >/dev/null
  local deadline=$(($(date +%s) + PHASES_TIMEOUT)) status
  while :; do
    # The Pod phase, not the Job status: the Job controller can lag by many minutes
    # while it works through thousands of load Jobs. A busy control plane can also
    # time out one poll; the next one decides.
    status="$(kubectl -n "${RUNNER_NAMESPACE}" get pods -l job-name=kube-burner -o jsonpath='{.items[0].status.phase}' || true)"
    case "${status}" in
      Succeeded) break ;;
      Failed) kubectl -n "${RUNNER_NAMESPACE}" logs job/kube-burner >"${RESULTS}/kube-burner.log" 2>&1 || true
              die "kube-burner failed; see ${RESULTS}/kube-burner.log" ;;
    esac
    [ "$(date +%s)" -lt "${deadline}" ] || die "kube-burner did not finish in ${PHASES_TIMEOUT}s"
    sleep 15
  done
  kubectl -n "${RUNNER_NAMESPACE}" logs job/kube-burner >"${RESULTS}/kube-burner.log" 2>&1 || true
}

run_sync() {
  phase sync
  wait_until "all ${EXPECTED_PODS} load Pods" "${PHASES_TIMEOUT}" pods_created
  result PODS_CREATED "$(date +%s)"
  wait_until "an exporter row for every load Pod" "${PHASES_TIMEOUT}" rows_match
  result ROWS_SYNCED "$(date +%s)"
}

# churn_cycle <n>: replaces a random share of the roots and fails a share of Running Pods.
churn_cycle() {
  local dir="${RESULTS}/churn" all picked clean total count
  mkdir -p "${dir}"
  all="${dir}/roots.json" picked="${dir}/picked-$1.json" clean="${dir}/create-$1.json"
  # JobSet and LWS copy the root label to their children; a root has no owner.
  retry kubectl get deployments.apps,jobs.batch,jobsets.jobset.x-k8s.io,leaderworkersets.leaderworkerset.x-k8s.io,pods \
    -A -l loadtest.karta/root=true -o json >"${dir}/roots-all.json"
  jq '.items |= map(select(.metadata.ownerReferences == null))' "${dir}/roots-all.json" >"${all}"
  total="$(jq '.items | length' "${all}")"
  count=$((total * PHASES_CHURN_PERCENT / 100))
  [ "${count}" -gt 0 ] || count=1
  # awk, not head: head exits early, and the SIGPIPE upstream fails the pipeline.
  jq -c '.items[]' "${all}" | awk 'BEGIN { srand() } { printf "%.9f\t%s\n", rand(), $0 }' |
    sort -k1,1 | awk -v n="${count}" 'NR <= n' | cut -f2- | jq -s '{apiVersion: "v1", kind: "List", items: .}' >"${picked}"
  jq '.items |= map(
        del(.status, .metadata.uid, .metadata.resourceVersion, .metadata.creationTimestamp,
            .metadata.generation, .metadata.managedFields,
            .metadata.annotations["deployment.kubernetes.io/revision"])
        | if .kind == "Job" then
            del(.spec.selector, .spec.template.metadata.labels["controller-uid"],
                .spec.template.metadata.labels["batch.kubernetes.io/controller-uid"],
                .spec.template.metadata.labels["job-name"],
                .spec.template.metadata.labels["batch.kubernetes.io/job-name"])
          elif .kind == "Pod" then del(.spec.nodeName) else . end
        # A new name per cycle, so the copy never waits for garbage collection of the
        # old children. Every string equal to the old name (selector and template
        # labels such as app: web-3) follows, so old and new selectors never overlap.
        | .metadata.name as $old
        | (($old | sub("-c[0-9]+$"; "")) + "-c'"$1"'") as $new
        | walk(if type == "string" and . == $old then $new else . end))' "${picked}" >"${clean}"
  log "  cycle $1: replace ${count} of ${total} roots"
  retry kubectl delete -f "${picked}" --cascade=background --wait=false --ignore-not-found >/dev/null
  # Server-side apply, not create: a retry after a partial success must not fail on
  # the copies that already exist.
  retry kubectl apply --server-side -f "${clean}" >/dev/null

  [ "${PHASES_CHURN_FAIL_PERCENT}" -gt 0 ] || return 0
  kubectl get pods -A -l '!loadtest.karta/root' --field-selector=status.phase=Running \
    -o jsonpath='{range .items[*]}{.metadata.namespace} {.metadata.name}{"\n"}{end}' |
    grep -E "^${LOAD_NS_REGEX} " >"${dir}/running.txt" || true
  count=$(($(wc -l <"${dir}/running.txt") * PHASES_CHURN_FAIL_PERCENT / 100))
  log "  cycle $1: fail ${count} Running Pods"
  awk 'BEGIN { srand() } { printf "%.9f\t%s\n", rand(), $0 }' "${dir}/running.txt" | sort -k1,1 |
    awk -v n="${count}" 'NR <= n' | cut -f2- | sort |
    awk '{ pods[$1] = pods[$1] " " $2 } END { for (ns in pods) print ns pods[ns] }' |
    while read -r ns names; do
      # Churn replaces Pods while this runs, so some can be gone already.
      # shellcheck disable=SC2086  # names is a space separated list on purpose
      kubectl -n "${ns}" annotate pod ${names} loadtest.karta/fail=true --overwrite >/dev/null 2>&1 || true
    done
}

run_churn() {
  [ "${PHASES_CHURN_PERCENT}" -gt 0 ] || return 0
  phase churn
  local end=$(($(date +%s) + PHASES_CHURN_DURATION)) cycle=1 started cpu
  # Covers the first cycle: its deletes, creates and failed Pods.
  cpu_profile churn "${PHASES_CHURN_CPU_PROFILE}" &
  cpu=$!
  while [ "$(date +%s)" -lt "${end}" ]; do
    started="$(date +%s)"
    churn_cycle "${cycle}"
    cycle=$((cycle + 1))
    wait_until_time $((started + PHASES_CHURN_DELAY))
  done
  wait "${cpu}" || true
  result CHURN_CYCLES $((cycle - 1))
  phase drain
  # Recreated Jobs restart from scratch, so wait for the Pod count, not only the queues.
  wait_until "empty exporter queues and matching rows" "${PHASES_TIMEOUT}" drained
  result DRAINED "$(date +%s)"
}

run_recover() {
  [ "${PHASES_RECOVER}" = "true" ] || return 0
  phase recover
  retry kubectl -n "${EXPORTER_NAMESPACE}" rollout restart "deploy/${EXPORTER_NAME}" >/dev/null
  retry kubectl -n "${EXPORTER_NAMESPACE}" rollout status "deploy/${EXPORTER_NAME}" --timeout="${PHASES_TIMEOUT}s" >/dev/null
  result EXPORTER_READY "$(date +%s)"
  local pod
  pod="$(exporter_pod)" || die "cannot read the exporter Pod"
  wait_until "rows from the new exporter Pod" "${PHASES_TIMEOUT}" rows_match "${pod}"
  result RECOVERED "$(date +%s)"
}

main() {
  plan_counts
  RESULTS="${LOADTEST_DIR}/${RESULTS_DIR}/${TIER}-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "${RESULTS}"
  set | grep -E '^(TIER|CLUSTER_NAME|SCALE_|MIX_|PODS_PER_WORKLOAD|CREATE_|PHASES_|EXPORTER_|DEPLOYMENTS|JOBS|JOBSETS|LWS|BARE_PODS|WORKLOADS|EXPECTED_PODS|KWOK_VERSION|KUBE_BURNER_IMAGE|KUBE_PROMETHEUS_STACK_VERSION)=' \
    >"${RESULTS}/config.env"
  log "tier ${TIER}: ${WORKLOADS} workloads, ${EXPECTED_PODS} Pods in ${SCALE_NAMESPACES} namespaces"
  log "per namespace: ${DEPLOYMENTS} Deployments, ${JOBS} Jobs, ${JOBSETS} JobSets, ${LWS} LWS, ${BARE_PODS} Pods"
  log "results: ${RESULTS}"
  port_forwards
  # PHASES_START=steady or churn reuses the workloads of an earlier run and skips
  # create and sync; churn also skips steady.
  case "${PHASES_START}" in
    create) run_create; run_sync ;;
    # A reused load still needs a settled exporter: steady must not include its startup sync.
    steady | churn) wait_until "the exporter to settle on every load Pod" "${PHASES_TIMEOUT}" exporter_settled ;;
    *) die "unknown PHASES_START ${PHASES_START}: use create, steady or churn" ;;
  esac
  if [ "${PHASES_START}" != "churn" ]; then
    phase steady
    local steady_end=$(($(date +%s) + PHASES_STEADY))
    # The CPU profile covers the last minute of steady, inside the phase.
    if [ "${PHASES_STEADY}" -gt 60 ]; then
      wait_until_time $((steady_end - 60))
      cpu_profile steady 60
    fi
    wait_until_time "${steady_end}"
    profiles steady
  fi
  run_churn
  ! grep -q '^DRAINED=' "${RESULTS}/phases.env" || profiles drained
  run_recover
  [ "${PHASES_RECOVER}" != "true" ] || profiles recovered
  phase report
  "${LOADTEST_DIR}/report.sh" "${RESULTS}"
}

main "$@"
