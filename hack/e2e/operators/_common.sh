# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Shared helpers for the per-operator e2e scripts under hack/e2e/operators/.
# Each operator ships a standalone install.sh (and, if it has a smoke.yaml, a
# verify.sh). Every such script sources this file once at its top:
#
#   MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "${MODULE_DIR}/../_common.sh"
#
# Sourcing this file also loads hack/e2e/global.env, so the version pins and
# runtime defaults are available without a second source. Reference co-located
# files via "${MODULE_DIR}/...". All helpers are bash 3.2 safe (no associative
# arrays, no mapfile).
# shellcheck shell=bash

# Global config + version pins in one place (resolved relative to this file).
_COMMON_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${_COMMON_DIR}/../global.env"

# --- GitHub Actions logging --------------------------------------------------
# Where output should go, so a run reads as a scannable report and not a wall of
# gray logs:
#   group/endgroup  wrap a phase's verbose command output; the run log collapses
#                   it, leaving the group title as the scannable line.
#   notice          a key milestone (blue annotation at the top of the run and on
#                   the PR); use rarely, one per run at most.
#   warn            a non-fatal problem worth surfacing (yellow annotation).
#   fail            a failure (red annotation, top of run + PR); pair with a
#                   non-zero exit.
#   summary         the run's report card: a markdown table written to the run's
#                   Summary page. This is the primary place to read results - the
#                   logs are only for drilling into detail.
# Under $GITHUB_ACTIONS these emit workflow commands; run locally they fall back
# to plain output so the same scripts read well in a terminal.
# See https://docs.github.com/actions/using-workflows/workflow-commands-for-github-actions

group() {
  if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::group::$*"; else echo "==> $*"; fi
}
endgroup() {
  if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::endgroup::"; fi
}
notice() {
  if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::notice::$*"; else echo "    $*"; fi
}
warn() {
  if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::warning::$*"; else echo "    warning: $*" >&2; fi
}
fail() {
  if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::error::$*"; else echo "    error: $*" >&2; fi
}

# summary <markdown-line>
# Append a line to the GitHub Actions job summary (rendered on the run page).
# A no-op when not running in Actions.
summary() {
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then printf '%s\n' "$*" >>"${GITHUB_STEP_SUMMARY}"; fi
}

# --- cluster helpers ---------------------------------------------------------

# rollout_wait <namespace> <resource> [timeout]
# Wait for a rollout to finish. <resource> includes the kind, e.g. deploy/foo or
# statefulset/foo, so this is not limited to Deployments. Default timeout 180s.
rollout_wait() {
  local ns="$1" resource="$2" timeout="${3:-180s}"
  kubectl -n "${ns}" rollout status "${resource}" --timeout="${timeout}"
}

# apply_with_retry <file-or-url> [tries] [sleep_secs] [extra kubectl apply args...]
# kubectl apply, retried while a validating webhook is still warming up. The apply
# runs inside an if so set -e does not abort on an expected transient failure.
apply_with_retry() {
  local target="$1"; shift
  # tries and sleep_secs are optional; consume a leading arg only if it is numeric,
  # so a caller can pass kubectl flags directly (apply_with_retry <url> --server-side).
  local tries=5 sleep_secs=10
  case "${1:-}" in '' | *[!0-9]*) ;; *) tries="$1"; shift ;; esac
  case "${1:-}" in '' | *[!0-9]*) ;; *) sleep_secs="$1"; shift ;; esac
  local i
  for i in $(seq 1 "${tries}"); do
    if kubectl apply "$@" -f "${target}"; then
      return 0
    fi
    echo "    apply failed (webhook warming up?); retry ${i}/${tries}" >&2
    sleep "${sleep_secs}"
  done
  return 1
}

# retry <tries> <sleep_secs> <command...>
# Run a command, retrying on failure past a transient (a webhook still warming up,
# an endpoint not yet reachable). The command runs inside an if so set -e does not
# abort on an expected transient failure.
retry() {
  local tries="$1" sleep_secs="$2"; shift 2
  local i
  for i in $(seq 1 "${tries}"); do
    if "$@"; then return 0; fi
    echo "    retry ${i}/${tries}: $*" >&2
    sleep "${sleep_secs}"
  done
  return 1
}

# run_smoke <manifest> <target> <wait-expr> [timeout] [namespace]
# Uniform smoke test: apply the throwaway resource (retried past webhook warmup),
# wait for the stable state, then delete. <wait-expr> is passed to kubectl wait
# --for=, so it handles both "condition=Ready" and "jsonpath={.status.x}=y".
run_smoke() {
  local manifest="$1" target="$2" wait_expr="$3" timeout="${4:-300s}" ns="${5:-default}"
  # Capture each step's result so the resource is always deleted (even if the apply
  # exhausts its retries) and the failure still propagates under set -e.
  local rc=0
  apply_with_retry "${manifest}" 6 10 || rc=$?
  if [ "${rc}" -eq 0 ]; then
    kubectl wait --for="${wait_expr}" "${target}" -n "${ns}" --timeout="${timeout}" || rc=$?
  fi
  kubectl delete -f "${manifest}" --wait=false --ignore-not-found || true
  return "${rc}"
}

# preload_image <source-ref> <local-tag>
# Pull an upstream image and load it into kind under a local tag, so the workload
# does not pull it mid-test.
preload_image() {
  local src="$1" tag="$2"
  docker pull "${src}"
  docker tag "${src}" "${tag}"
  kind load docker-image "${tag}" --name "${CLUSTER_NAME}"
}

# build_and_load_image <context-dir> <local-tag>
# Build a local image from a build context and load it into kind.
build_and_load_image() {
  local ctx="$1" tag="$2" go_version=""
  # An image that builds Go takes the version its go.mod asks for.
  [ -f "${ctx}/go.mod" ] && go_version=$(awk '/^go /{print $2; exit}' "${ctx}/go.mod")
  docker build ${go_version:+--build-arg "GO_VERSION=${go_version}"} -t "${tag}" "${ctx}"
  kind load docker-image "${tag}" --name "${CLUSTER_NAME}"
}

# ensure_secret <namespace> <name> <literal k=v> [more k=v...]
# Idempotently create/update an opaque secret via apply.
ensure_secret() {
  local ns="$1" name="$2"; shift 2
  local args=() kv
  for kv in "$@"; do args+=(--from-literal="${kv}"); done
  kubectl create secret generic "${name}" -n "${ns}" "${args[@]}" \
    --dry-run=client -o yaml | kubectl apply -f -
}
