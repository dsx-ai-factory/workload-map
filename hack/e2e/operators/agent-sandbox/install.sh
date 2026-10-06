#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Install the Kubernetes SIG Agent Sandbox controller and its Sandbox CRD from the
# pinned release manifest.
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${MODULE_DIR}/../_common.sh"

main() {
  echo "==> Agent Sandbox ${AGENT_SANDBOX_VERSION}"
  kubectl apply --server-side -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/sandbox.yaml"
  rollout_wait agent-sandbox-system deploy/agent-sandbox-controller
}

main "$@"
