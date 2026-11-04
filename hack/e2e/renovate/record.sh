#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

: "${E2E_UPDATE_ARTIFACTS:?}"
: "${E2E_UPDATE_BASE_SHA:?}"
: "${E2E_UPDATE_GROUP:?}"
: "${E2E_UPDATE_RUN_URL:?}"
[ "${GITHUB_ACTIONS:-}" = true ] || { echo 'Recording updates requires GitHub Actions' >&2; exit 1; }
REPO_ROOT=$(git rev-parse --show-toplevel)
cd "${REPO_ROOT}"
ARTIFACTS="${E2E_UPDATE_ARTIFACTS}"
# A hook may be retried by Renovate. Never accept evidence from an earlier attempt.
[ ! -e "${ARTIFACTS}/record-started" ] || { echo 'Recording already attempted in this job' >&2; exit 1; }
touch "${ARTIFACTS}/record-started"
python3 hack/e2e/renovate/candidate.py prepare --artifacts "${ARTIFACTS}" --base "${E2E_UPDATE_BASE_SHA}"

# Ignore runner overrides so the installed versions come from Renovate's edited file.
while IFS= read -r variable; do unset "${variable}"; done < <(sed -n 's/^\([A-Z_]*_VERSION\)=.*/\1/p' hack/e2e/global.env)
unset FLOW E2E_FOCUS E2E_LABELS KIND_NODE_IMAGE
# shellcheck source=hack/e2e/global.env
source hack/e2e/global.env
export CLUSTER_NAME="renovate-${E2E_UPDATE_GROUP}-${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
export KUBECONFIG="${RUNNER_TEMP:?}/renovate-${E2E_UPDATE_GROUP}-kubeconfig"
workloads=$(jq -r '.workloads | join(" ")' "${ARTIFACTS}/plan.json")
labels=$(jq -r '.workloads | join(",")' "${ARTIFACTS}/plan.json")
webhook=$(jq -r '.webhookMode' "${ARTIFACTS}/plan.json")
check_dir=""
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [ "${status}" -ne 0 ]; then
    kind export logs "${ARTIFACTS}/kind" --name "${CLUSTER_NAME}" > "${ARTIFACTS}/kind-export.log" 2>&1 || true
  fi
  kind delete cluster --name "${CLUSTER_NAME}" > "${ARTIFACTS}/cleanup.log" 2>&1 || status=1
  rm -f "${KUBECONFIG}" || status=1
  if [ -n "${check_dir}" ]; then
    chmod -R u+w "${check_dir}" || status=1
    rm -rf "${check_dir}" || status=1
  fi
  if [ "${status}" -eq 0 ]; then
    python3 hack/e2e/renovate/candidate.py manifest --artifacts "${ARTIFACTS}" || status=1
  fi
  if [ "${status}" -ne 0 ]; then
    rm -f "${ARTIFACTS}/manifest.json"
  fi
  exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
clusters=$(kind get clusters)
if printf '%s\n' "${clusters}" | grep -Fxq "${CLUSTER_NAME}"; then
  echo 'The recording requires a fresh cluster' >&2
  exit 1
fi

run_logged() {
  local log="$1"
  shift
  if ! "$@" > "${ARTIFACTS}/${log}.log" 2>&1; then
    tail -n 100 "${ARTIFACTS}/${log}.log"
    return 1
  fi
}

git diff -- hack/e2e/global.env > "${ARTIFACTS}/pins.patch"
run_logged install make e2e-up "WORKLOADS=${workloads}" "CLUSTER_NAME=${CLUSTER_NAME}" "KARTA_WEBHOOK_MODE=${webhook}"
installed="hack/e2e/operators/.installed-versions-${CLUSTER_NAME}"
python3 hack/e2e/renovate/candidate.py installed --artifacts "${ARTIFACTS}" --installed "${installed}"
cp "${installed}" "${ARTIFACTS}/installed-versions"
kubectl version -o json | jq '{serverVersion}' > "${ARTIFACTS}/kubernetes.json"
kubectl get pods -A -o json | jq '[.items[] | {namespace:.metadata.namespace, pod:.metadata.name, containers:[.status.containerStatuses[]? | {image,imageID,ready}]}]' > "${ARTIFACTS}/images.json"
if [ "${webhook}" = cert-manager ]; then
  run_logged operator make test-operator-e2e "CLUSTER_NAME=${CLUSTER_NAME}" "KARTA_WEBHOOK_MODE=${webhook}"
fi
(
  cd test/e2e
  go test -count=1 ./flows -args -ginkgo.dry-run -ginkgo.label-filter="${labels}" -ginkgo.junit-report="${ARTIFACTS}/expected.xml"
) > "${ARTIFACTS}/discovery.log" 2>&1
run_logged recording make record-e2e "WORKLOADS=${workloads}" "CLUSTER_NAME=${CLUSTER_NAME}" "E2E_JUNIT_REPORT=${ARTIFACTS}/actual.xml"
(
  cd test/e2e
  go run ./cmd/verify-operator-update -expected-junit "${ARTIFACTS}/expected.xml" \
    -junit "${ARTIFACTS}/actual.xml" -installed-versions "${ARTIFACTS}/installed-versions" -workloads "${labels}"
) | tee "${ARTIFACTS}/verification.log"
python3 hack/e2e/renovate/candidate.py headers --artifacts "${ARTIFACTS}"
run_logged replay make verify-recordings test-replay

# Presubmit checks require committed files. Keep the published commit owned by Renovate.
check_dir=$(mktemp -d)
git clone --quiet --shared "${REPO_ROOT}" "${check_dir}"
python3 hack/e2e/renovate/candidate.py copy --artifacts "${ARTIFACTS}" --destination "${check_dir}"
git -C "${check_dir}" config user.name 'CI validation'
git -C "${check_dir}" config user.email 'ci@example.com'
git -C "${check_dir}" add -- hack/e2e/global.env test/e2e/recorded_data
git -C "${check_dir}" -c commit.gpgsign=false commit --quiet -s -m 'test(e2e): validate operator update recordings'
run_logged make-check make -C "${check_dir}" check "VERSION=0.0.0-main-${E2E_UPDATE_BASE_SHA:0:7}"
printf 'Validated %s in %s\n' "${E2E_UPDATE_GROUP}" "${E2E_UPDATE_RUN_URL}"
