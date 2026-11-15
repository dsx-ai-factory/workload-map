#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

if [ "$#" -lt 3 ] || [ -z "$1" ] || [ -z "$2" ] || [ -z "$3" ]; then
  echo "Usage: $0 <release> <namespace> <values-file> [extra helm args...]" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
release="$1"
namespace="$2"
values_file="$3"
shift 3

installed=false
if helm status "$release" -n "$namespace" >/dev/null 2>&1; then
  installed=true
fi
helm upgrade --install "$release" "$ROOT/charts/karta" \
  --namespace "$namespace" --create-namespace \
  -f "$values_file" --timeout 3m "$@"
# Restart before waiting so old unready Pods cannot block the rebuilt image.
if "$installed"; then
  kubectl -n "$namespace" rollout restart deployment/dsx-workload-exporter
fi
kubectl -n "$namespace" rollout status deployment/dsx-workload-exporter --timeout=180s
