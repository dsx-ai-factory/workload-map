#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# cert-manager, required by KServe or Karta's cert-manager webhook mode.
# shellcheck disable=SC2154  # CERT_MANAGER_VERSION comes from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../../operators/_common.sh"

main() {
  echo "==> cert-manager ${CERT_MANAGER_VERSION}"
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
  local deployment
  for deployment in cert-manager cert-manager-webhook cert-manager-cainjector; do
    rollout_wait cert-manager "deploy/${deployment}"
  done
}

main "$@"
