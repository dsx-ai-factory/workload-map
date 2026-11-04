#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hack/e2e/_updates.sh
source "${MODULE_DIR}/../../_updates.sh"

# Latest Trainer tags can replace the installed v1 APIs, requiring migration review.
# https://github.com/kubeflow/trainer/blob/658725bdae5c7e3a0854d93ac6ff492e109a22ab/docs/operator-guides/migration.md
check_updates "$@" \
  --operator "kubeflow" "${KUBEFLOW_VERSION}" "kubeflow/trainer" "v" "stable" "Trainer v2 replaces the v1 training APIs; review migration before changing KUBEFLOW_VERSION." \
  --operator "mpi-operator" "${MPI_OPERATOR_VERSION}" "kubeflow/mpi-operator" "v" "stable" ""
