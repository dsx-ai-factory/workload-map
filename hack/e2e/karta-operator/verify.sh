#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke test for the Karta install: a throwaway Karta must reach Ready. Same
# install.sh/verify.sh pair the workload operators use.
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../operators/_common.sh"

echo "==> smoke: karta/karta-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "karta/karta-smoke" "condition=Ready" "120s" default
