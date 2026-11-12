#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Install the OpenKruise controller-manager via Helm.
set -euo pipefail
# shellcheck source=/dev/null
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/_common.sh"

helm repo add openkruise https://openkruise.github.io/charts/ 2>/dev/null || true
helm repo update openkruise
helm upgrade --install kruise openkruise/kruise \
  --version "${OPENKRUISE_VERSION}" \
  --namespace kruise-system --create-namespace \
  --wait --timeout 180s
