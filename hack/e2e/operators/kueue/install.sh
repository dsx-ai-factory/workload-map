#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

helm upgrade --install kueue oci://registry.k8s.io/kueue/charts/kueue \
  --version "${KUEUE_VERSION#v}" --namespace kueue-system --create-namespace \
  --values "${MODULE_DIR}/values.yaml" --wait --timeout 5m
apply_with_retry "${MODULE_DIR}/queue.yaml" --server-side
