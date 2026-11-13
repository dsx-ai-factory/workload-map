#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke test for Argo Rollouts: a throwaway Rollout must reach phase=Healthy.
# shellcheck disable=SC2154  # ARGO_ROLLOUTS_VERSION comes from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

echo "==> smoke: rollouts.argoproj.io/rollout-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "rollouts.argoproj.io/rollout-smoke" \
  "jsonpath={.status.phase}=Healthy" "300s" default
