#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Fake GPU Operator supplies simulated GPUs on the kind workers.
# shellcheck disable=SC2154  # FAKE_GPU_VERSION comes from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../../operators/_common.sh"

main() {
  echo "==> fake-gpu-operator ${FAKE_GPU_VERSION}"
  # Select every worker so additional kind workers receive simulated GPUs too.
  kubectl label nodes -l '!node-role.kubernetes.io/control-plane' \
    run.ai/simulated-gpu-node-pool=default --overwrite
  helm upgrade -i fake-gpu-operator oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
    -n gpu-operator --create-namespace --version "${FAKE_GPU_VERSION}" \
    --set computeDomainDraPlugin.enabled=true --wait --timeout 3m >/dev/null
}

main "$@"
