#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Run the operator e2e against the current cluster. Provision first with up.sh;
# this script installs nothing.
# shellcheck disable=SC2154  # KARTA_* come from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../operators/_common.sh"

REPO_ROOT="${REPO_ROOT:-$(cd "${MODULE_DIR}/../../.." && pwd)}"
ARTIFACTS="${ARTIFACTS:-${REPO_ROOT}/.artifacts}"
E2E_OPERATOR_TIMEOUT="${E2E_OPERATOR_TIMEOUT:-15m}"

# On every exit path, not just failure: a trap that only fires on error is one
# nobody tests.
collect() {
  local rc=$?
  mkdir -p "${ARTIFACTS}"
  group "diagnostics"
  kubectl -n "${KARTA_NAMESPACE}" logs "deploy/${KARTA_FULLNAME}" --all-containers --tail=-1 \
    >"${ARTIFACTS}/${KARTA_FULLNAME}.log" 2>&1 || true
  kubectl -n "${KARTA_NAMESPACE}" describe "deploy/${KARTA_FULLNAME}" \
    >"${ARTIFACTS}/${KARTA_FULLNAME}.describe" 2>&1 || true
  kubectl get kartas -o yaml >"${ARTIFACTS}/kartas.yaml" 2>&1 || true
  kubectl get events -A --sort-by=.lastTimestamp >"${ARTIFACTS}/events.txt" 2>&1 || true
  kubectl get mutatingwebhookconfigurations,validatingwebhookconfigurations -o yaml \
    >"${ARTIFACTS}/webhooks.yaml" 2>&1 || true
  echo "==> diagnostics in ${ARTIFACTS}"
  endgroup
  return "${rc}"
}
trap collect EXIT

echo "==> operator e2e (webhook: ${KARTA_WEBHOOK_MODE})"
cd "${REPO_ROOT}/operator"
# -count=1 keeps a previous pass from being replayed from the cache.
KARTA_WEBHOOK_MODE="${KARTA_WEBHOOK_MODE}" \
KARTA_NAMESPACE="${KARTA_NAMESPACE}" \
KARTA_FULLNAME="${KARTA_FULLNAME}" \
  go test -tags e2e -count=1 -v -timeout "${E2E_OPERATOR_TIMEOUT}" ./test/e2e/...
