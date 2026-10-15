#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Install the Kubernetes SIG Agent Sandbox controller and its Sandbox CRD from the
# pinned release manifest.
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

# Script-level so the EXIT trap sees it under set -u.
manifest=""
trap '[ -n "${manifest}" ] && rm -f "${manifest}"' EXIT

main() {
  echo "==> Agent Sandbox ${AGENT_SANDBOX_VERSION}"
  manifest="$(mktemp)"
  curl -fsSL "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/sandbox.yaml" -o "${manifest}"
  # The release manifest lists the controller Deployment before the Sandbox CRD, and the
  # controller registers its watches on the CRD at startup. Apply the CRD documents first.
  awk 'BEGIN { RS = "---\n" } /kind: CustomResourceDefinition/ { printf "---\n%s", $0 }' "${manifest}" \
    | kubectl apply --server-side -f -
  kubectl wait --for=condition=Established crd/sandboxes.agents.x-k8s.io --timeout=60s
  kubectl apply --server-side -f "${manifest}"
  rollout_wait agent-sandbox-system deploy/agent-sandbox-controller
}

main "$@"
