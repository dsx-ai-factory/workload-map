#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# SDK .postN and undashed rcN tags are not controller release candidates.
# https://github.com/kubernetes-sigs/agent-sandbox/blob/82d410efd5a279e887cdcf8c01e742a345fef63d/Makefile#L199
check_updates "$@" \
  --operator "agent-sandbox" "${AGENT_SANDBOX_VERSION}" "kubernetes-sigs/agent-sandbox" "v" "stable" ""
