#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke-test the OpenKruise CRD: a throwaway CloneSet must reach readyReplicas=1.
# shellcheck disable=SC2154  # OPENKRUISE_VERSION comes from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

echo "==> smoke: clonesets.apps.kruise.io/cloneset-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "clonesets.apps.kruise.io/cloneset-smoke" \
  "jsonpath={.status.readyReplicas}=1" "120s" default
