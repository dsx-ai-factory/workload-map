# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
# shellcheck shell=bash

_UPDATES_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/global.env
source "${_UPDATES_DIR}/global.env"

prepare_update_checker() {
  if [ -z "${E2E_OPERATOR_UPDATES_BIN:-}" ]; then
    command -v go >/dev/null || { echo "missing required tool: go" >&2; return 1; }
    local root
    root="$(cd "${_UPDATES_DIR}/../.." && pwd)"
    mkdir -p "${root}/bin" || return 1
    # Build once for the aggregate invocation and share the executable with its wrappers.
    if ! (cd "${root}/hack/operator-updates" && GOWORK=off go build -o "${root}/bin/operator-updates" .); then
      echo "failed to build the operator update checker" >&2
      return 1
    fi
    E2E_OPERATOR_UPDATES_BIN="${root}/bin/operator-updates"
    export E2E_OPERATOR_UPDATES_BIN
  fi
  if [ ! -f "${E2E_OPERATOR_UPDATES_BIN}" ] || [ ! -x "${E2E_OPERATOR_UPDATES_BIN}" ]; then
    echo "operator update checker is not executable: ${E2E_OPERATOR_UPDATES_BIN}" >&2
    return 1
  fi
}

check_updates() {
  prepare_update_checker || return 1
  "${E2E_OPERATOR_UPDATES_BIN}" "$@"
}
