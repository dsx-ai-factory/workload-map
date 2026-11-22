#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# Stable root tags are promoted; the Helm chart version drops their leading v.
# https://github.com/kubernetes-sigs/kueue/blob/7e71578095b353cedbfa63607b60c9739af9d501/hack/helm-chart-package.sh
check_updates "$@" \
  --operator "kueue" "${KUEUE_VERSION}" "kubernetes-sigs/kueue" "v" "stable" ""
