#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# Track chart-releaser tags; application and config-tool tags identify other artifacts.
# https://github.com/zilliztech/milvus-operator/blob/3ae2cbf219db098a588552f05902c80fe82ebf29/.github/workflows/release.yml
check_updates "$@" \
  --operator "milvus" "${MILVUS_OPERATOR_VERSION}" "zilliztech/milvus-operator" "milvus-operator-" "stable" "Tracks the published Helm chart tags."
