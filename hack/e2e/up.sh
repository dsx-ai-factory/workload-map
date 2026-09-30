#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Provision a local kind cluster for the Karta e2e suite: builds and deploys the
# Karta operator, installs the base dependencies (fake-gpu-operator, plus
# cert-manager when something needs it), and installs the selected upstream workload
# operators, smoke-testing each one as it installs. KARTA_WEBHOOK_MODE picks which
# webhook and serving-cert arrangement Karta is installed with.
# Run with --help for usage; see hack/e2e/README.md for the details.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OPERATORS_DIR="${REPO_ROOT}/hack/e2e/operators"
# Shared helpers + GitHub Actions logging. Sourcing this also loads
# hack/e2e/global.env (version pins and the CLUSTER_NAME/IMAGE runtime defaults).
# shellcheck source=/dev/null
source "${OPERATORS_DIR}/_common.sh"

DEFAULT_CLUSTER="karta-e2e"
# CLUSTER_NAME and IMAGE come from global.env (overridable from the environment).
# Each non-default cluster gets its own kubeconfig, so several clusters (for
# example two CI shards installing different operators) can be provisioned in
# parallel without racing on the shared current-context. The default cluster keeps
# the standard kubeconfig so plain kubectl works after e2e-up.
if [ -z "${KUBECONFIG:-}" ] && [ "${CLUSTER_NAME}" != "${DEFAULT_CLUSTER}" ]; then
  KUBECONFIG="${HOME}/.kube/kind-${CLUSTER_NAME}.kubeconfig"
  mkdir -p "$(dirname "${KUBECONFIG}")"
  export KUBECONFIG
fi
# karta-operator/ and operators/<name>/ each run install.sh and verify.sh as
# subprocesses; export what they need (version pins come from global.env).
export CLUSTER_NAME IMAGE REPO_ROOT KARTA_WEBHOOK_MODE

# Workload operators selectable on the command line, in canonical install order:
# a dependency always appears before its dependents (knative before kserve,
# grove before dynamo).
ALL_WORKLOADS=(lws jobset kuberay kubeflow knative kserve milvus grove dynamo nim)

# deps_of <workload> prints the workload operators that must be installed first.
deps_of() {
  case "$1" in
    kserve) echo "knative" ;;
    dynamo) echo "grove" ;;
  esac
}

# version_of <workload> prints its pinned version, for the job-summary table.
version_of() {
  case "$1" in
    lws) echo "${LWS_VERSION}" ;;
    jobset) echo "${JOBSET_VERSION}" ;;
    kuberay) echo "${KUBERAY_VERSION}" ;;
    kubeflow) echo "${KUBEFLOW_VERSION}+mpi${MPI_OPERATOR_VERSION}" ;;
    knative) echo "${KNATIVE_VERSION}" ;;
    kserve) echo "${KSERVE_VERSION}" ;;
    milvus) echo "${MILVUS_OPERATOR_VERSION}" ;;
    grove) echo "${GROVE_VERSION}" ;;
    dynamo) echo "${DYNAMO_VERSION}" ;;
    nim) echo "${NIM_OPERATOR_VERSION}" ;;
    *) echo "?" ;;
  esac
}

usage() {
  cat >&2 <<EOF
Usage: $0 [--list] [workload...]
  No workload args (or "all") installs everything. Named args install the base
  plus only those workload operators (and their dependencies). "none" installs
  the base only, which is what the operator e2e wants.
  Workloads: ${ALL_WORKLOADS[*]}
  --list    print the resolved install plan and exit

Environment (see hack/e2e/global.env):
  KARTA_WEBHOOK_MODE  auto | cert-manager | disabled   (default auto)
  CERT_MANAGER        auto | true | false              (default auto)
EOF
}

# --- base (always installed) -------------------------------------------------

# require_tools fails fast with a clear message if a needed binary is missing,
# rather than erroring cryptically partway through provisioning.
require_tools() {
  local missing=()
  for t in docker kind kubectl helm curl tar; do
    command -v "$t" >/dev/null 2>&1 || missing+=("$t")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    echo "error: missing required tools: ${missing[*]}" >&2
    echo "install them and re-run." >&2
    exit 1
  fi
}

setup_cluster() {
  echo "==> build operator image (${IMAGE})"
  # Build output is left visible: a failing image build is the first thing to debug.
  docker build --build-arg "GO_VERSION=${GO_VERSION}" \
    -f "${REPO_ROOT}/operator/Dockerfile" -t "${IMAGE}" "${REPO_ROOT}"

  if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
    echo "==> reusing existing kind cluster (${CLUSTER_NAME})"
    # Re-export the kubeconfig: the cluster can outlive its context (a reset or
    # overwritten ~/.kube/config), and "kind create" is skipped on this path.
    kind export kubeconfig --name "${CLUSTER_NAME}" >/dev/null
  else
    echo "==> create kind cluster (${CLUSTER_NAME}, ${KIND_NODE_IMAGE})"
    kind create cluster --name "${CLUSTER_NAME}" --image "${KIND_NODE_IMAGE}" \
      --config "${REPO_ROOT}/hack/e2e/kind-config.yaml"
  fi
  # Pin kubectl to this cluster so every step below targets it, not a stale context.
  kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null
  echo "==> load operator image"
  kind load docker-image "${IMAGE}" --name "${CLUSTER_NAME}"
  kubectl wait --for=condition=Ready nodes --all --timeout=120s
}

install_cert_manager() {
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
  for d in cert-manager cert-manager-webhook cert-manager-cainjector; do
    rollout_wait cert-manager "deploy/${d}"
  done
}

install_fake_gpu() {
  # Label every worker (by role selector, not a hardcoded node name) so this still
  # works if kind-config.yaml grows more workers.
  kubectl label nodes -l '!node-role.kubernetes.io/control-plane' \
    run.ai/simulated-gpu-node-pool=default --overwrite
  # --wait so later steps do not race a not-yet-ready operator.
  helm upgrade -i fake-gpu-operator oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
    -n gpu-operator --create-namespace --version "${FAKE_GPU_VERSION}" \
    --set computeDomainDraPlugin.enabled=true --wait --timeout 3m >/dev/null
}

# --- selectable workload operators -------------------------------------------

# run_operator <name>: run the operator's standalone install.sh then verify.sh,
# each as a subprocess, grouped in the CI log and recording a job-summary row.
# Exits non-zero on the first failure so a broken operator fails provisioning fast
# rather than partway through the e2e suite.
run_operator() {
  local name="$1" dir="${OPERATORS_DIR}/$1"
  local ver
  ver="$(version_of "${name}")"
  [ -f "${dir}/install.sh" ] || { fail "no install.sh for operator ${name}"; exit 1; }
  group "operator: ${name} (${ver})"
  # SECONDS is a bash builtin counting elapsed seconds; diff it to time each phase.
  local t0=$SECONDS irc=0
  bash "${dir}/install.sh" || irc=$?
  local idur=$((SECONDS - t0))
  if [ "${irc}" -ne 0 ]; then
    endgroup
    echo "==> ${name}: install FAILED after ${idur}s"
    # :x: renders as a red X in the GitHub summary; ASCII in the source.
    summary "| :x: | ${name} | ${ver} | fail (${idur}s) | - |"
    fail "install ${name} failed (exit ${irc}, ${idur}s)"
    exit 1
  fi
  local smoke="n/a" vdur=0
  if [ -f "${dir}/verify.sh" ]; then
    local t1=$SECONDS src=0
    bash "${dir}/verify.sh" || src=$?
    vdur=$((SECONDS - t1))
    if [ "${src}" -ne 0 ]; then
      endgroup
      echo "==> ${name}: smoke FAILED after ${vdur}s"
      summary "| :x: | ${name} | ${ver} | ${idur}s | fail (${vdur}s) |"
      fail "smoke ${name} failed (exit ${src}, ${vdur}s)"
      exit 1
    fi
    smoke="${vdur}s"
  fi
  endgroup
  # Ungrouped, so the outcome is visible without expanding the group above.
  echo "==> ${name}: ready (install ${idur}s, smoke ${smoke})"
  summary "| :white_check_mark: | ${name} | ${ver} | ${idur}s | ${smoke} |"
  # The e2e flows file each recording under the installed operator version. Per cluster,
  # like the kubeconfig, so parallel clusters with different versions cannot mix.
  echo "${name}=${ver}" >> "${OPERATORS_DIR}/.installed-versions-${CLUSTER_NAME}"
}

main() {
  local plan_only=false
  local requested=()
  for arg in "$@"; do
    case "$arg" in
      -h | --help) usage; exit 0 ;;
      --list) plan_only=true ;;
      -*) echo "unknown flag: $arg" >&2; usage; exit 2 ;;
      *) requested+=("$arg") ;;
    esac
  done

  # "all" is an alias for every workload (same as passing no args). Guard the
  # expansion so bare "up.sh" does not trip set -u on the empty array (bash 3.2).
  local base_only=false
  if [ "${#requested[@]}" -gt 0 ]; then
    for w in "${requested[@]}"; do
      if [ "$w" = "none" ]; then
        [ "${#requested[@]}" -eq 1 ] ||
          { echo "error: \"none\" cannot be combined with other workloads" >&2; usage; exit 2; }
        base_only=true
      fi
    done
    for w in "${requested[@]}"; do
      [ "$w" = "all" ] && { requested=("${ALL_WORKLOADS[@]}"); break; }
    done
  fi

  local selected=()
  if [ "${base_only}" = true ]; then
    selected=()
  elif [ "${#requested[@]}" -eq 0 ]; then
    selected=("${ALL_WORKLOADS[@]}")
  else
    for w in "${requested[@]}"; do
      printf '%s\n' "${ALL_WORKLOADS[@]}" | grep -qxF "$w" ||
        { echo "unknown workload: $w" >&2; usage; exit 2; }
    done
    selected=("${requested[@]}")
    for w in "${requested[@]}"; do
      for d in $(deps_of "$w"); do selected+=("$d"); done
    done
  fi

  # Build the ordered plan by walking the canonical order and keeping selected ones.
  local plan=()
  if [ "${#selected[@]}" -gt 0 ]; then
    for w in "${ALL_WORKLOADS[@]}"; do
      if printf '%s\n' "${selected[@]}" | grep -qxF "$w"; then plan+=("$w"); fi
    done
  fi

  case "${KARTA_WEBHOOK_MODE}" in
    auto | cert-manager | disabled) ;;
    *)
      echo "error: unknown KARTA_WEBHOOK_MODE '${KARTA_WEBHOOK_MODE}' (want: auto, cert-manager, disabled)" >&2
      exit 2
      ;;
  esac

  local cert_manager_needed_by=""
  [ "${KARTA_WEBHOOK_MODE}" = "cert-manager" ] && cert_manager_needed_by="KARTA_WEBHOOK_MODE=cert-manager"
  for w in ${plan[@]+"${plan[@]}"}; do
    if [ "$w" = "kserve" ]; then
      cert_manager_needed_by="${cert_manager_needed_by:+${cert_manager_needed_by}, }operator ${w}"
    fi
  done
  local install_cm=false
  case "${CERT_MANAGER}" in
    auto) [ -n "${cert_manager_needed_by}" ] && install_cm=true ;;
    true) install_cm=true ;;
    false)
      if [ -n "${cert_manager_needed_by}" ]; then
        echo "error: CERT_MANAGER=false but cert-manager is required by: ${cert_manager_needed_by}" >&2
        exit 2
      fi
      ;;
    *)
      echo "error: unknown CERT_MANAGER '${CERT_MANAGER}' (want: auto, true, false)" >&2
      exit 2
      ;;
  esac

  if [ "$plan_only" = true ]; then
    echo "base: kind cluster, fake-gpu-operator, karta (webhook: ${KARTA_WEBHOOK_MODE})"
    if [ "${install_cm}" = true ]; then
      echo "cert-manager: yes (${cert_manager_needed_by:-CERT_MANAGER=true})"
    else
      echo "cert-manager: no"
    fi
    if [ "${#plan[@]}" -gt 0 ]; then echo "workloads: ${plan[*]}"; else echo "workloads: (none)"; fi
    exit 0
  fi

  require_tools
  group "build image + kind cluster"; setup_cluster; endgroup
  if [ "${install_cm}" = false ] && kubectl get namespace cert-manager >/dev/null 2>&1; then
    warn "cert-manager is already on ${CLUSTER_NAME}; not installing it does not remove it"
  fi
  if [ "${install_cm}" = true ]; then
    group "cert-manager ${CERT_MANAGER_VERSION}"; install_cert_manager; endgroup
  fi
  group "fake-gpu-operator ${FAKE_GPU_VERSION}"; install_fake_gpu; endgroup
  # Fresh provision, fresh version list (gitignored; read by the e2e flows).
  : > "${OPERATORS_DIR}/.installed-versions-${CLUSTER_NAME}"
  if [ "${#plan[@]}" -gt 0 ]; then
    summary "## E2E install: ${CLUSTER_NAME}"
    summary ""
    summary "|  | Operator | Version | Install | Smoke |"
    summary "|---|---|---|---|---|"
    for w in "${plan[@]}"; do run_operator "$w"; done
  fi
  group "karta operator"
  bash "${REPO_ROOT}/hack/e2e/karta-operator/install.sh" || { endgroup; fail "karta install failed"; exit 1; }
  bash "${REPO_ROOT}/hack/e2e/karta-operator/verify.sh" || { endgroup; fail "karta smoke failed"; exit 1; }
  endgroup

  echo "==> environment ready (cluster: ${CLUSTER_NAME}, webhook: ${KARTA_WEBHOOK_MODE})."
  if [ "${CLUSTER_NAME}" != "${DEFAULT_CLUSTER}" ]; then
    echo "    this cluster has its own kubeconfig: ${KUBECONFIG}"
    echo "    export KUBECONFIG=${KUBECONFIG} to use kubectl against it"
  fi
}

main "$@"
