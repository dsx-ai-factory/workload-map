#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Install the Karta operator from the local chart, in the arrangement
# KARTA_WEBHOOK_MODE selects. Run standalone against the current context, or via
# up.sh, which runs it last.
# shellcheck disable=SC2154  # KARTA_* and IMAGE come from global.env via _common.sh
set -euo pipefail
MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${MODULE_DIR}/../operators/_common.sh"

# REPO_ROOT is exported by up.sh; derive it when this script is run on its own.
REPO_ROOT="${REPO_ROOT:-$(cd "${MODULE_DIR}/../../.." && pwd)}"

# Suffixes the chart puts on karta.fullname (charts/karta/templates/_helpers.tpl). The
# service name is also the serving-cert SAN, so the Certificate below has to match it.
KARTA_WEBHOOK_SERVICE="${KARTA_FULLNAME}-webhook"
KARTA_WEBHOOK_SECRET="${KARTA_FULLNAME}-webhook-cert"
KARTA_WEBHOOK_CONFIGS="mutatingwebhookconfiguration/${KARTA_FULLNAME}-mutating validatingwebhookconfiguration/${KARTA_FULLNAME}-validating"
# Not chart-rendered: this is the Certificate this script creates, so it does not follow
# the fullname.
KARTA_WEBHOOK_CERT="karta-webhook-cert"

# The chart ships no Issuer or Certificate, so provisionMode=manual is only installable
# if the caller supplies them. This has to run before the helm install rather than from
# a test: controller-runtime reads the serving cert at startup, so a pod that starts
# without the Secret crashloops instead of waiting for it.
install_certificate() {
  kubectl create namespace "${KARTA_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  local manifest
  manifest="$(mktemp)"
  cat >"${manifest}" <<EOF
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: karta-selfsigned
  namespace: ${KARTA_NAMESPACE}
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: ${KARTA_WEBHOOK_CERT}
  namespace: ${KARTA_NAMESPACE}
spec:
  secretName: ${KARTA_WEBHOOK_SECRET}
  issuerRef:
    name: karta-selfsigned
    kind: Issuer
  dnsNames:
    - ${KARTA_WEBHOOK_SERVICE}.${KARTA_NAMESPACE}.svc
    - ${KARTA_WEBHOOK_SERVICE}.${KARTA_NAMESPACE}.svc.cluster.local
EOF
  apply_with_retry "${manifest}" >/dev/null
  rm -f "${manifest}"
  kubectl wait --for=condition=Ready "certificate/${KARTA_WEBHOOK_CERT}" \
    -n "${KARTA_NAMESPACE}" --timeout=120s
}

# Runs after the helm install, since cainjector only acts on configs that already carry
# the annotation. Nothing else gates on it: auto mode's rotator writes the caBundle
# before the operator reports ready, but in manual mode cainjector is asynchronous, so
# without this the first admission call can fail x509 against an empty caBundle.
wait_for_ca_injection() {
  local target ca want
  want="$(kubectl get secret "${KARTA_WEBHOOK_SECRET}" -n "${KARTA_NAMESPACE}" \
    -o jsonpath='{.data.ca\.crt}')"
  [ -n "${want}" ] || { fail "no ca.crt in ${KARTA_WEBHOOK_SECRET}"; exit 1; }
  for target in ${KARTA_WEBHOOK_CONFIGS}; do
    kubectl get "${target}" -o name >/dev/null
    ca=""
    for _ in $(seq 1 60); do
      ca="$(kubectl get "${target}" -o jsonpath='{.webhooks[0].clientConfig.caBundle}')"
      [ "${ca}" = "${want}" ] && break
      sleep 2
    done
    if [ "${ca}" != "${want}" ]; then
      fail "cainjector did not put the issued CA on ${target} within 120s"
      exit 1
    fi
  done
}

# The mode the deployed release runs, from the args the chart renders per mode, or
# nothing when Karta is not installed.
installed_webhook_mode() {
  local args
  args="$(kubectl get "deploy/${KARTA_FULLNAME}" -n "${KARTA_NAMESPACE}" \
    -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null)" || return 0
  [ -n "${args}" ] || return 0
  case "${args}" in
    *webhook-cert-mode=auto*) echo auto ;;
    *webhook-cert-mode=manual*) echo cert-manager ;;
    *) echo disabled ;;
  esac
}

main() {
  echo "==> Karta operator (webhook: ${KARTA_WEBHOOK_MODE})"

  local installed
  installed="$(installed_webhook_mode)"
  if [ -n "${installed}" ] && [ "${installed}" != "${KARTA_WEBHOOK_MODE}" ]; then
    fail "webhook mode changed on an existing cluster (${installed} -> ${KARTA_WEBHOOK_MODE}); run make e2e-down first"
    exit 1
  fi

  kubectl apply --server-side -f "${REPO_ROOT}/charts/karta/crds/"

  local webhook_values=()
  case "${KARTA_WEBHOOK_MODE}" in
    auto)
      webhook_values=(--set webhook.enabled=true --set webhook.cert.provisionMode=auto)
      ;;
    cert-manager)
      install_certificate
      webhook_values=(
        --set webhook.enabled=true
        --set webhook.cert.provisionMode=manual
        --set-string "webhook.cert.annotations.cert-manager\.io/inject-ca-from=${KARTA_NAMESPACE}/${KARTA_WEBHOOK_CERT}"
      )
      ;;
    disabled)
      webhook_values=(--set webhook.enabled=false)
      ;;
    *)
      echo "error: unknown KARTA_WEBHOOK_MODE '${KARTA_WEBHOOK_MODE}' (want: auto, cert-manager, disabled)" >&2
      exit 2
      ;;
  esac

  helm upgrade -i karta "${REPO_ROOT}/charts/karta" -n "${KARTA_NAMESPACE}" --create-namespace \
    --set image.repository="${IMAGE%:*}" --set image.tag="${IMAGE##*:}" \
    --set resources.limits.memory="${KARTA_OPERATOR_MEMORY}" \
    "${webhook_values[@]}" >/dev/null
  rollout_wait "${KARTA_NAMESPACE}" "deploy/${KARTA_FULLNAME}" 120s
  [ "${KARTA_WEBHOOK_MODE}" = "cert-manager" ] && wait_for_ca_injection
  return 0
}

main "$@"
