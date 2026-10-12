#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

check_updates "$@" \
  --operator "fake-gpu-operator" "${FAKE_GPU_VERSION}" "run-ai/fake-gpu-operator" "v" "stable" "Git vX.Y.Z maps to OCI Helm chart X.Y.Z."
