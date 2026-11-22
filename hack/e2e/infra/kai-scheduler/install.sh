#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# KAI Scheduler provides Grove's gang-scheduling backend.
# shellcheck disable=SC2154  # KAI_SCHEDULER_VERSION comes from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../../operators/_common.sh"

main() {
  echo "==> kai-scheduler ${KAI_SCHEDULER_VERSION}"
  helm upgrade -i kai-scheduler oci://ghcr.io/kai-scheduler/kai-scheduler/kai-scheduler \
    --version "${KAI_SCHEDULER_VERSION}" -n kai-scheduler --create-namespace --wait --timeout 5m >/dev/null
}

main "$@"
