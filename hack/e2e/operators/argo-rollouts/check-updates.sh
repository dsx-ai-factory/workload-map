#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# Argo Rollouts publishes GitHub releases with a v prefix. The install.yaml
# release asset is the pinned artifact (not a Helm chart).
# https://github.com/argoproj/argo-rollouts/releases
check_updates "$@" \
  --operator "argo-rollouts" "${ARGO_ROLLOUTS_VERSION}" "argoproj/argo-rollouts" "v" "stable" "Tracks GitHub releases; install.yaml is the release asset, not a Helm chart."
