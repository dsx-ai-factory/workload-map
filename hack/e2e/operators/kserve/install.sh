#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# KServe, Serverless on Knative + Kourier. Depends on knative (see deps_of in
# up.sh). Ships a config patch (disable-istio-vh.yaml).
# shellcheck disable=SC2154  # KSERVE_VERSION/GATEWAY_API_VERSION come from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../_common.sh"

main() {
  echo "==> KServe ${KSERVE_VERSION} (Serverless on Knative + Kourier)"
  # The release manifest ships no Namespace: KServe's generator strips it and
  # leaves it to the dependency installer.
  kubectl create namespace kserve --dry-run=client -o yaml | kubectl apply -f -
  # kserve.yaml also starts the LLMInferenceService controller, which registers
  # its HTTPRoute and Gateway watches only if those CRDs exist at startup, so
  # the Gateway API goes first. The InferencePool CRDs ship inside kserve.yaml.
  kubectl apply --server-side -f \
    "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
  # The manifest carries instances of its own CRDs (a ClusterStorageContainer),
  # which the first pass rejects until the CRDs are established, so retry.
  # --force-conflicts: cert-manager-cainjector owns the webhook caBundle fields,
  # and on a reused cluster the inferenceservice-config patch below already owns
  # the ingress keys. Reclaim them here; the patch re-asserts its override.
  apply_with_retry "https://github.com/kserve/kserve/releases/download/${KSERVE_VERSION}/kserve.yaml" \
    3 5 --server-side --force-conflicts
  # Serverless KServe defaults to creating Istio VirtualServices; without Istio the
  # reconcile errors and PredictorReady/RoutesReady never go True. Route through
  # Knative/Kourier instead.
  kubectl patch cm inferenceservice-config -n kserve --type merge \
    --patch-file "${MODULE_DIR}/disable-istio-vh.yaml"
  rollout_wait kserve deploy/kserve-controller-manager 240s
  # kserve-cluster-resources.yaml carries the LLMInferenceServiceConfig presets
  # next to the ClusterServingRuntimes, and both are admitted by webhooks with
  # failurePolicy Fail, so every webhook pod must be Ready before applying it
  # (releases before v0.19 ship no llmisvc controller); retry briefly in case
  # a webhook is still warming.
  if kubectl get deploy -n kserve llmisvc-controller-manager >/dev/null 2>&1; then
    rollout_wait kserve deploy/llmisvc-controller-manager 240s
  fi
  # A Ready webhook pod does not prove cert-manager injected its CA into the
  # webhook configurations yet; wait for the certificates to be issued.
  kubectl -n kserve wait --for=condition=Ready certificate --all --timeout=180s
  apply_with_retry "https://github.com/kserve/kserve/releases/download/${KSERVE_VERSION}/kserve-cluster-resources.yaml" \
    5 10 --server-side --force-conflicts
}

main "$@"
