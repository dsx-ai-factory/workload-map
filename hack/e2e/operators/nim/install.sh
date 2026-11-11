#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# k8s-nim-operator with a fictive CPU NIM image (no GPU or NGC token needed). Ships
# an image build context (image/). The operator chart is not published to a Helm/OCI
# registry, so it is fetched at install time from the pinned upstream git tag
# (NIM_OPERATOR_VERSION) rather than vendored in-repo.
# shellcheck disable=SC2154  # Version and image pins come from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

main() {
  echo "==> fake NIM image + k8s-nim-operator"
  local image_repository="nvcr.io/nvidia/cloud-native/k8s-nim-operator"
  local operator_image="${image_repository}:${NIM_OPERATOR_IMAGE_TAG}"
  if [ "${NIM_OPERATOR_IMAGE_TAG%%@*}" != "${NIM_OPERATOR_VERSION}" ]; then
    echo "error: NIM_OPERATOR_IMAGE_TAG must match NIM_OPERATOR_VERSION (${NIM_OPERATOR_VERSION}); provide that release's verified tag and digest" >&2
    exit 1
  fi
  local image_digest="${NIM_OPERATOR_IMAGE_TAG#*@sha256:}"
  if [[ ! "${image_digest}" =~ ^[a-f0-9]{64}$ ]]; then
    echo "error: NIM_OPERATOR_IMAGE_TAG must include an immutable sha256 digest" >&2
    exit 1
  fi
  local installed=false actual_image
  if helm status k8s-nim-operator -n nim-operator >/dev/null 2>&1; then
    actual_image=$(kubectl -n nim-operator get deployment k8s-nim-operator \
      -o 'jsonpath={.spec.template.spec.containers[?(@.name=="manager")].image}')
    if [ "${actual_image}" != "${operator_image}" ]; then
      echo "error: existing NIM operator image differs from the configured release pin; use a fresh e2e cluster or explicitly reconcile that installation" >&2
      exit 1
    fi
    installed=true
  fi
  build_and_load_image "${MODULE_DIR}/image" nim-cpu:e2e
  # The dev chart ships incomplete RBAC (cannot list computedomains/ingress/hpa/lws ->
  # cache-sync crashloop), so grant the operator broad access on this throwaway cluster.
  kubectl create clusterrolebinding k8s-nim-operator-admin \
    --clusterrole=cluster-admin --serviceaccount=nim-operator:k8s-nim-operator \
    --dry-run=client -o yaml | kubectl apply -f -
  # Install only when absent: the chart's pre-upgrade CRD-migration hook Job
  # crashloops when re-run, so plain "helm upgrade -i" is not idempotent here.
  if [ "${installed}" = true ]; then
    echo "    k8s-nim-operator release already present; skipping reinstall"
  else
    # The operator chart is not published to a Helm/OCI registry, only in-repo,
    # so fetch it from the pinned upstream tag at install time instead of
    # vendoring it. The tag tarball extracts to k8s-nim-operator-<version>/.
    # src is not local so the EXIT trap can resolve it under set -u (cleans up even
    # if a step below fails).
    local chart="" d
    src="$(mktemp -d)"
    trap 'rm -rf "${src}"' EXIT
    curl -fsSL "https://github.com/NVIDIA/k8s-nim-operator/archive/refs/tags/${NIM_OPERATOR_VERSION}.tar.gz" \
      | tar -xz -C "${src}"
    for d in "${src}"/k8s-nim-operator-*/deployments/helm/k8s-nim-operator; do
      [ -d "${d}" ] && chart="${d}"
    done
    if [ -z "${chart}" ]; then
      echo "error: k8s-nim-operator Helm chart not found in the ${NIM_OPERATOR_VERSION} tarball (upstream layout changed?)" >&2
      exit 1
    fi
    # The tagged chart defaults to a mutable development image, so pin the
    # official release image independently of the chart archive.
    helm install k8s-nim-operator "${chart}" -n nim-operator --create-namespace \
      --set-string "operator.image.repository=${image_repository}" \
      --set-string "operator.image.tag=${NIM_OPERATOR_IMAGE_TAG}" >/dev/null
  fi
  rollout_wait nim-operator deploy/k8s-nim-operator
  # Dummy NGC secret: the operator injects NGC_API_KEY from it; the fictive image ignores it.
  ensure_secret default ngc-secret NGC_API_KEY=dummy-not-a-real-token
}

main "$@"
