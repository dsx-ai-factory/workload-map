#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke test for Volcano: a throwaway Volcano Job must reach Completed.
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

echo "==> smoke: jobs.batch.volcano.sh/vcjob-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "jobs.batch.volcano.sh/vcjob-smoke" "jsonpath={.status.state.phase}=Completed" "120s" default
