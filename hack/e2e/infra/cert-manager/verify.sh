#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke test for cert-manager: issue a certificate into a throwaway namespace.
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../../operators/_common.sh"

echo "==> smoke: certificate/cert-manager-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "certificate/cert-manager-smoke" "condition=Ready" "120s" cert-manager-smoke
