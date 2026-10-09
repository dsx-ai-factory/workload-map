#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Smoke test for KServe: a throwaway InferenceService, and an LLMInferenceService
# where the release ships its controller, must reach Ready.
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

echo "==> smoke: isvc/kserve-smoke"
run_smoke "${MODULE_DIR}/smoke.yaml" "isvc/kserve-smoke" "condition=Ready" "300s" default

# Releases before v0.19 ship no llmisvc controller (see install.sh).
if kubectl get deploy -n kserve llmisvc-controller-manager >/dev/null 2>&1; then
  echo "==> smoke: llminferenceservice/kserve-llmisvc-smoke"
  run_smoke "${MODULE_DIR}/llmisvc-smoke.yaml" "llminferenceservice/kserve-llmisvc-smoke" "condition=Ready" "300s" default
fi
