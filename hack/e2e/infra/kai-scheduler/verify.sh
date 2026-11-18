#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke test for KAI Scheduler: a CPU-only pod must be scheduled and become ready.
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../../operators/_common.sh"

echo "==> smoke: pod/kai-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "pod/kai-smoke" "condition=Ready" "300s" default
