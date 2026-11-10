#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Volcano operator (batch.volcano.sh/v1alpha1 Job).
# shellcheck disable=SC2154  # VOLCANO_VERSION comes from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

main() {
  echo "==> Volcano ${VOLCANO_VERSION}"
  kubectl apply --server-side --force-conflicts -f "https://raw.githubusercontent.com/volcano-sh/volcano/${VOLCANO_VERSION}/installer/volcano-development.yaml"
  rollout_wait volcano-system deploy/volcano-controllers
  rollout_wait volcano-system deploy/volcano-scheduler
}

main "$@"
