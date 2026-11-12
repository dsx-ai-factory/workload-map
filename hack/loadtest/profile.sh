#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Capture Go profiles from the running exporter (needs exporter.pprof.enabled, which
# up.sh sets): a CPU profile over PROFILE_SECONDS, plus heap, allocs and goroutine.
# Usage: profile.sh <label> [results dir]. The files go to <results>/pprof/<label>-*.pb.gz;
# the default results dir is the newest run. Open one with make loadtest-flamegraph.
set -euo pipefail
# shellcheck source=hack/loadtest/common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

label="${1:?usage: profile.sh <label> [results dir]}"
results="${2:-$(find "${LOADTEST_DIR}/${RESULTS_DIR}" -mindepth 1 -maxdepth 1 -type d -name '*-2*' 2>/dev/null | sort | tail -n 1)}"
[ -n "${results}" ] || die "no results directory; pass one"
seconds="${PROFILE_SECONDS:-30}"
# Not EXPORTER_PPROF_PORT: run.sh uses that one for its own captures at phase ends.
port="${PROFILE_PORT:-16061}"
dir="${results}/pprof"
mkdir -p "${dir}"

kubectl -n "${EXPORTER_NAMESPACE}" port-forward "deploy/${EXPORTER_NAME}" "${port}:6060" >/dev/null 2>&1 &
pf=$!
trap 'kill "${pf}" 2>/dev/null || true' EXIT
wait_until "the pprof port-forward" 60 curl -fsS -o /dev/null "http://localhost:${port}/debug/pprof/"

log "${label}: CPU profile for ${seconds}s"
curl -fsS -m $((seconds + 60)) -o "${dir}/${label}-cpu.pb.gz" "http://localhost:${port}/debug/pprof/profile?seconds=${seconds}"
for kind in heap allocs goroutine; do
  curl -fsS -m 120 -o "${dir}/${label}-${kind}.pb.gz" "http://localhost:${port}/debug/pprof/${kind}"
done
log "${label}: saved to ${dir}"
ls -1 "${dir}/${label}-"*
