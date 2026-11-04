#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# Root releases include alpha.N; ignore the separate Go module tags.
# https://github.com/ai-dynamo/grove/blob/f038137374627c4921b398ab395d8789de686e89/.github/workflows/release.yaml
check_updates "$@" \
  --operator "grove" "${GROVE_VERSION}" "ai-dynamo/grove" "v" "alpha" "Includes alpha releases; release candidates require --include-prereleases. Check KAI Scheduler compatibility."
