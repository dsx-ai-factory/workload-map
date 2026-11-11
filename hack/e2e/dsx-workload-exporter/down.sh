#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail
kind delete cluster --name "${EXPORTER_E2E_CLUSTER:-dsx-exporter-e2e}"
