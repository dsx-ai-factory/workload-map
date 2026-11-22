#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# OpenKruise publishes GitHub releases with a v prefix. The Helm chart version
# matches the release tag without the v prefix.
# https://github.com/openkruise/kruise/releases
check_updates "$@" \
  --operator "openkruise" "${OPENKRUISE_VERSION}" "openkruise/kruise" "v" "stable" "Tracks GitHub releases; installed via Helm chart from openkruise.github.io/charts/."
