#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Argo Rollouts controller (github.com/argoproj/argo-rollouts).
# Installs the namespace-scoped manifest from the GitHub release asset.
# shellcheck disable=SC2154  # ARGO_ROLLOUTS_VERSION comes from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

# Trusted SHA-256 digest for each pinned version's install.yaml.
# Add a new case when bumping ARGO_ROLLOUTS_VERSION in global.env.
trusted_digest() {
  case "$1" in
    v1.10.2) echo "b8280994791eaee7eb91c8b84450efcc6e0292fca5aafc18c1b0ebc5d18dbefe" ;;
    *) return 1 ;;
  esac
}

TMPDIR_CLEANUP=""
trap 'rm -rf "${TMPDIR_CLEANUP}"' EXIT

main() {
  echo "==> Argo Rollouts ${ARGO_ROLLOUTS_VERSION}"

  local digest
  if ! digest="$(trusted_digest "${ARGO_ROLLOUTS_VERSION}")"; then
    echo "ERROR: no trusted digest for Argo Rollouts ${ARGO_ROLLOUTS_VERSION}" >&2
    echo "Add one to trusted_digest() in $(basename "$0") after verifying the manifest." >&2
    exit 1
  fi

  TMPDIR_CLEANUP="$(mktemp -d)"
  local manifest="${TMPDIR_CLEANUP}/install.yaml"

  curl -fsSL -o "${manifest}" \
    "https://github.com/argoproj/argo-rollouts/releases/download/${ARGO_ROLLOUTS_VERSION}/install.yaml"

  local actual
  actual="$(shasum -a 256 "${manifest}" | awk '{print $1}')"
  if [[ "${actual}" != "${digest}" ]]; then
    echo "ERROR: digest mismatch for Argo Rollouts ${ARGO_ROLLOUTS_VERSION}" >&2
    echo "  expected: ${digest}" >&2
    echo "  got:      ${actual}" >&2
    exit 1
  fi

  kubectl create namespace argo-rollouts 2>/dev/null || true
  apply_with_retry "${manifest}" 6 10 --server-side --force-conflicts -n argo-rollouts
  rollout_wait argo-rollouts deploy/argo-rollouts 120s
}

main "$@"
