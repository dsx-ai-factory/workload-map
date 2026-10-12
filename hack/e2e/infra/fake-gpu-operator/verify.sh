#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke test for Fake GPU Operator: a Pod requesting a simulated GPU must run.
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../../operators/_common.sh"

echo "==> smoke: pod/fake-gpu-operator-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "pod/fake-gpu-operator-smoke" "condition=Ready" "180s" default
