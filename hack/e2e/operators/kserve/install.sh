#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# KServe, Serverless on Knative + Kourier. Depends on knative (see deps_of in
# up.sh). Ships a config patch (disable-istio-vh.yaml).
# shellcheck disable=SC2154  # KSERVE_VERSION/KUBE_RBAC_PROXY_VERSION come from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

main() {
  echo "==> KServe ${KSERVE_VERSION} (Serverless on Knative + Kourier)"
  # --force-conflicts: cert-manager-cainjector owns the webhook caBundle fields,
  # and on a reused cluster our own set-image/patch steps below already own the
  # rbac-proxy image and inferenceservice-config ingress. Reclaim them here; the
  # set-image and patch steps that follow re-assert those overrides.
  kubectl apply --server-side --force-conflicts -f "https://github.com/kserve/kserve/releases/download/${KSERVE_VERSION}/kserve.yaml"
  # Upstream pins gcr.io/kubebuilder/kube-rbac-proxy:${KSERVE_VERSION}, a tag that
  # registry no longer serves; the sidecar only guards metrics, so repoint it to a
  # maintained image, otherwise the pod never goes Ready and the webhook has no
  # endpoints.
  kubectl set image deployment/kserve-controller-manager -n kserve \
    kube-rbac-proxy="quay.io/brancz/kube-rbac-proxy:${KUBE_RBAC_PROXY_VERSION}"
  # Serverless KServe defaults to creating Istio VirtualServices; without Istio the
  # reconcile errors and PredictorReady/RoutesReady never go True. Route through
  # Knative/Kourier instead.
  kubectl patch cm inferenceservice-config -n kserve --type merge \
    --patch-file "${MODULE_DIR}/disable-istio-vh.yaml"
  rollout_wait kserve deploy/kserve-controller-manager 240s
  # ClusterServingRuntimes are validated by the webhook, so apply them only after
  # the controller pod is Ready; retry briefly in case the webhook is still warming.
  apply_with_retry "https://github.com/kserve/kserve/releases/download/${KSERVE_VERSION}/kserve-cluster-resources.yaml" \
    5 10 --server-side --force-conflicts
  install_llmisvc
}

# LLMInferenceService ships as its own controller and charts (releases that
# publish helm-chart-kserve-llmisvc-resources). Its manager watches HTTPRoute
# and InferencePool, so the Gateway API and inference-extension CRDs must exist
# before it starts.
install_llmisvc() {
  local base="https://github.com/kserve/kserve/releases/download/${KSERVE_VERSION}"
  if ! curl -fsIL -o /dev/null "${base}/helm-chart-kserve-llmisvc-resources-${KSERVE_VERSION}.tgz"; then
    echo "    release ships no llmisvc charts; skipping the LLMInferenceService controller"
    return 0
  fi
  echo "==> KServe llmisvc controller ${KSERVE_VERSION}"
  kubectl apply --server-side -f \
    "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
  kubectl apply --server-side -f \
    "https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/${GATEWAY_API_INFERENCE_EXTENSION_VERSION}/v1-manifests.yaml"
  # The minimal CRD chart carries only the llmisvc CRDs; the full one also
  # packages shared KServe CRDs that kserve.yaml already owns, and helm refuses
  # to adopt those.
  helm upgrade -i kserve-llmisvc-crd "${base}/helm-chart-kserve-llmisvc-crd-minimal-${KSERVE_VERSION}.tgz" \
    -n kserve >/dev/null
  helm upgrade -i kserve-llmisvc-resources "${base}/helm-chart-kserve-llmisvc-resources-${KSERVE_VERSION}.tgz" \
    -n kserve >/dev/null
  rollout_wait kserve deploy/llmisvc-controller-manager 240s
}

main "$@"
