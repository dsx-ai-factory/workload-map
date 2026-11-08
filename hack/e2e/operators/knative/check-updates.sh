#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

check_updates "$@" \
  --operator "knative" "${KNATIVE_VERSION}" "knative/serving" "knative-v" "stable" "Review Serving and Kourier compatibility together." \
  --operator "kourier" "${KOURIER_VERSION}" "knative-extensions/net-kourier" "knative-v" "stable" ""
