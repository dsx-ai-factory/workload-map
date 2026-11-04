#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# The installed operator chart has its own namespace in this multi-chart repository.
# https://github.com/ray-project/kuberay-helm/blob/51711514ae948c9bea2c2440bdee3708f5625ee5/.github/workflows/chart-release.yaml
check_updates "$@" \
  --operator "kuberay" "${KUBERAY_VERSION}" "ray-project/kuberay-helm" "kuberay-operator-" "stable" "Tracks the published operator chart, not Ray runtime or application tags."
