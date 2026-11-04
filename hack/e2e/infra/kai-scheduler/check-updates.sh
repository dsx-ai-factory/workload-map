#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# Upstream retracts these versions; exclude them even if charts remain available.
# https://github.com/kai-scheduler/KAI-Scheduler/blob/173748a0b254531a5054374580e34ec8b9d839c8/go.mod#L220
check_updates "$@" --exclude-tag v0.20.0 --exclude-tag v0.20.1 \
  --operator "kai-scheduler" "${KAI_SCHEDULER_VERSION}" "kai-scheduler/KAI-Scheduler" "v" "stable" "Excludes retracted v0.20.0/v0.20.1. Check Grove compatibility."
