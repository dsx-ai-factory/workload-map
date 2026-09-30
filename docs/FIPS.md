<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# FIPS 140-3

The Karta operator image is built with Go's native [FIPS 140-3
support](https://go.dev/doc/security/fips140) (`GOFIPS140=v1.0.0`), so all
`crypto/*` operations are served by the CMVP-validated Go Cryptographic Module.
This is a single image: the FIPS module is always linked in, and the
`global.fipsMode` chart value only controls how strictly it is enforced at runtime.
There is no separate `-fips` image variant.

## Setting the mode

```yaml
global:
  fipsMode: "off"
```

`global.fipsMode` sets `GODEBUG=fips140=<mode>` on the operator container and
on the `crd-upgrader` pre-install/pre-upgrade hook Job. This is a runtime
switch, not a build-time one. `GOFIPS140=v1.0.0` always links the FIPS module
into the operator image, regardless of `global.fipsMode`. Valid values:

- `off` (default) - FIPS mode disabled at runtime; the module is present in
  the binary but not engaged, and no self-tests run.
- `on` - the FIPS module is used and runs its startup self-tests, but
  non-approved algorithms are still allowed (advisory mode).
- `only` - non-approved algorithms are rejected. See below before using this
  in production.

```sh
helm upgrade --install karta oci://ghcr.io/dsx-ai-factory/workload-map/karta \
  -n karta-system --create-namespace --set global.fipsMode=only
```

## `only` mode is a testing aid, not a production mode

Per the [`GODEBUG=fips140` option
docs](https://go.dev/doc/security/fips140#the-fips140-godebug-option),
`fips140=only` is a best-effort diagnostic for testing, assessment, and
debugging. Upstream Go explicitly does not recommend it for production. When a
non-approved cryptographic algorithm is used, the Go FIPS 140-3 module can
return an error or panic at the call site, depending on the code path; a
panic crashes the pod instead of degrading gracefully. Using `only` is the
caller's responsibility: test it against your cluster's actual configuration
before relying on it, and do not treat it as a substitute for `on` in
production.

The operator's own crypto usage was tested under `fips140=only` against a real
cluster: client-go's TLS connection to the API server, and the webhook's
serving certificate bootstrap (RSA-2048 + `x509.CreateCertificate`, via
`open-policy-agent/cert-controller`). Neither required disabling the default
`X25519MLKEM768` TLS 1.3 hybrid curve (`GODEBUG=tlsmlkem=0`), a workaround
some other Go services need under `fips140=only` because the curve's
implementation calls a non-approved plain X25519 primitive internally (see
[golang/go#78298](https://github.com/golang/go/issues/78298) and
[kubernetes/kubernetes#133743](https://github.com/kubernetes/kubernetes/issues/133743)).
A future Kubernetes or Go version could change that negotiation. If the
operator starts failing outbound TLS handshakes under `fips140=only`, the
fix is to also set `tlsmlkem=0` on the operator container. That requires
updating the chart's `deployment.yaml`. There is no values-based override
for it today. The `GODEBUG` value is hardcoded from `global.fipsMode`, and
`extraArgs` only appends container arguments, not environment variables.

The `crd-upgrader` Job is a separate case. Its default image
(`crdUpgrader.image`, `registry.k8s.io/kubectl`) is a stock `kubectl` build,
not built with `GOFIPS140`. `GODEBUG=fips140=only` still restricts that
binary to approved algorithms at runtime, but without the CMVP-certified
module backing the restriction, that is not the same as a FIPS 140-3
validated crypto implementation. Whether a given `kubectl` build's TLS
handshake trips the restriction depends on which Go toolchain version built
it: testing found one build that failed during OpenAPI schema validation,
which `tlsmlkem=0` fixed. Because that risk could not be ruled out for any
given build, `crd-upgrader` always sets `GODEBUG=fips140=only,tlsmlkem=0`
(not just `fips140=only`) when `global.fipsMode` is `only`.

No FIPS-built `kubectl` image is published upstream today. If `crd-upgrader`
needs to run a CMVP-certified module for your compliance requirements,
supply your own FIPS-built image via `crdUpgrader.image`.
