#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ALL_OPERATORS=(cert-manager fake-gpu-operator kai-scheduler lws jobset kuberay kubeflow knative kserve milvus grove dynamo nim agent-sandbox)

usage() {
  cat <<EOF
Usage: $0 [--fail-on-update] [--include-prereleases] [operator...]
  No names (or all) checks every workload and infrastructure operator.
  Operators: ${ALL_OPERATORS[*]}
  infra checks cert-manager, fake-gpu-operator, and kai-scheduler.
  Kubeflow also checks MPI Operator; Knative also checks Kourier.
  --fail-on-update       exit 2 if any newer tags exist (default: report only)
  --include-prereleases  include prerelease tags (Grove includes alpha releases by default)
  Lookup failures exit 1, even when other checks found updates.
EOF
}

main() {
  local fail_on_update=false requested=() flags=(--no-header --fail-on-update)
  local arg name candidate valid rc report failed=false updated=()
  for arg in "$@"; do
    case "$arg" in
      -h|--help) usage; return 0 ;;
      --fail-on-update) fail_on_update=true ;;
      --include-prereleases) flags+=("$arg") ;;
      all) requested+=("${ALL_OPERATORS[@]}") ;;
      infra) requested+=(cert-manager fake-gpu-operator kai-scheduler) ;;
      -*) echo "unknown flag: $arg" >&2; usage >&2; return 1 ;;
      *) requested+=("$arg") ;;
    esac
  done
  if [ "${#requested[@]}" -eq 0 ]; then requested=("${ALL_OPERATORS[@]}"); fi
  for name in "${requested[@]}"; do
    valid=false
    for candidate in "${ALL_OPERATORS[@]}"; do
      if [ "$candidate" = "$name" ]; then valid=true; break; fi
    done
    if [ "$valid" = false ]; then echo "unknown operator: $name" >&2; usage >&2; return 1; fi
  done
  command -v git >/dev/null || { echo "missing required tool: git" >&2; return 1; }
  # shellcheck source=hack/e2e/_updates.sh
  source "${E2E_DIR}/_updates.sh"
  prepare_update_checker || return 1
  echo 'Candidate versions only: upstream support and Karta compatibility are not verified.'
  printf '%-8s %-20s %-28s %s\n' STATUS OPERATOR PINNED 'LATEST TAG'
  for name in "${ALL_OPERATORS[@]}"; do
    if ! printf '%s\n' "${requested[@]}" | grep -qxF "$name"; then continue; fi
    case "$name" in
      cert-manager|fake-gpu-operator|kai-scheduler) candidate="${E2E_DIR}/infra/${name}/check-updates.sh" ;;
      *) candidate="${E2E_DIR}/operators/${name}/check-updates.sh" ;;
    esac
    rc=0
    report="$(bash "$candidate" "${flags[@]}")" || rc=$?
    printf '%s\n' "$report"
    # A group can contain both NEW and ERROR rows (for example Trainer plus MPI).
    if printf '%s\n' "$report" | grep -q '^NEW '; then updated+=("$name"); fi
    case "$rc" in
      0|2) ;;
      *) failed=true ;;
    esac
  done
  echo
  if [ "${#updated[@]}" -gt 0 ]; then
    echo "New tags in operator groups: ${updated[*]}"
  else
    echo "No newer tags found by successful checks."
  fi
  if [ "$failed" = true ]; then
    echo "One or more checks failed; the report is incomplete." >&2
    return 1
  fi
  if [ "$fail_on_update" = true ] && [ "${#updated[@]}" -gt 0 ]; then return 2; fi
}

main "$@"
