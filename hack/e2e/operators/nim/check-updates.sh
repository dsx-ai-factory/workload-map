#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# install.sh downloads a Git tag; Chart.yaml is not the release-version source.
# Upstream sets the chart version when packaging:
# https://github.com/NVIDIA/k8s-nim-operator/blob/53173fde86c7252215aae44750f2636c26332e73/Makefile#L170
check_updates "$@" \
  --operator "nim" "${NIM_OPERATOR_VERSION}" "NVIDIA/k8s-nim-operator" "v" "stable" "Tracks the git archive used by install.sh; chart metadata can lag the repository tag."
