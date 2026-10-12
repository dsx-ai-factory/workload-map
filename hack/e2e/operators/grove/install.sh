#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Install Grove after up.sh provisions and verifies its KAI Scheduler dependency.
# Dynamo depends on this module (see deps_of in up.sh). Ships a values.yaml.
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

main() {
  echo "==> Grove ${GROVE_VERSION}"
  # Trim scheduler profiles to the backends actually installed (default-scheduler +
  # kai); the chart default also lists volcano/lpx, whose absent CRDs crash the
  # operator at startup. The chart installs the Grove CRDs itself.
  helm upgrade -i grove-operator oci://ghcr.io/ai-dynamo/grove/grove-charts \
    --version "${GROVE_VERSION}" -n grove-system --create-namespace \
    -f "${MODULE_DIR}/values.yaml" --wait --timeout 5m >/dev/null
  rollout_wait grove-system deploy/grove-operator
  # Health probes are enabled in values.yaml, so the rollout above only completes
  # once the webhook is serving - the smoke apply will not race it.
}

main "$@"
