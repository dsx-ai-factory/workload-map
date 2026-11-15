#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

exec bash "$(dirname "${BASH_SOURCE[0]}")/hack/demo-grafana-exporter/up.sh" "$@"
