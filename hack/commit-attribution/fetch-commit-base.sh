#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

set -euo pipefail

if [[ $# -ne 1 || ! ${1:-} =~ ^[0-9a-f]{40}$ ]]; then
  echo "Usage: $0 <event-base-sha>" >&2
  exit 2
fi

# A force-push can leave the previous branch tip outside the fetched history.
if [[ $1 != 0000000000000000000000000000000000000000 ]] &&
   ! git rev-parse --verify --end-of-options "$1^{commit}" >/dev/null 2>&1; then
  git fetch --no-tags origin "$1"
fi
