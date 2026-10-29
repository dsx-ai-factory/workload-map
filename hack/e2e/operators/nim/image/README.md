<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Fictive CPU NIM image

A small stand-in for an NVIDIA NIM container, used by the Karta e2e cluster so the
k8s-nim-operator can drive a NIMService to Ready without a GPU or a real NGC token.
It is a Go HTTP server (see `main.go`) answering the endpoints the operator probes,
in particular `GET /v1/health/ready`, so the operator drives the NIMService to
`state=Ready`. It serves dummy responses only; it loads no model and performs no
inference.

The server uses the Go standard library only and ships as a static binary on a
nonroot distroless base (see `Dockerfile`). The image has no shell or Python
runtime. The base image and Go runtime still require normal security updates.

## What Karta uses

The operator only needs the readiness endpoint to mark the NIMService ready:

- HTTP on port 8000.
- `GET /v1/health/ready` and `GET /v1/health/live` for health probing.
- `GET /v1/models`, `POST /v1/chat/completions`, `GET /v1/metrics` for
  completeness; the Karta suite does not exercise these.

## Cache jobs

The image also includes `create-model-store`, a CPU stub for the universal NIM
cache command in [NIM operator v3.0.2](https://github.com/NVIDIA/k8s-nim-operator/blob/1cc1750182177c4186713f0ac7c4194f836676ee/internal/controller/nimcache_controller.go#L1206).
The NIMCache fixtures set `source.ngc.modelEndpoint`, which skips profile
selection and manifest inspection in that release, then lets the real controller
create a PVC and a Job. The installer pins the official operator image separately
from the chart, whose default image is a mutable development tag. The stub writes
`fictive-model.txt` to the PVC. It does not contact the model endpoint or use
credentials. The existing HTTP server remains the image entrypoint.

`NIM_CACHE_TEST_DELAY` accepts a Go duration such as `15s`, allowing the recorder
to observe the active Job. `NIM_CACHE_TEST_FAIL=true` makes the command exit with
an error before writing its marker. The controller reports Failed when it sees
a failed Job Pod, which can precede exhausted Job retries.
The native `InProgress` state is based on `Job.status.active`, which includes
Pending Pods; it does not establish that a container has started.

## How the suite builds it

`hack/e2e/up.sh` builds and loads the image into the kind cluster:

```sh
docker build --build-arg GO_VERSION="$(awk '/^go /{print $2; exit}' hack/e2e/operators/nim/image/go.mod)" \
  -t nim-cpu:e2e hack/e2e/operators/nim/image
kind load docker-image nim-cpu:e2e --name karta-e2e
```

The NIMService smoke test (`hack/e2e/operators/nim/smoke.yaml`) references
`nim-cpu:e2e`, and `up.sh` creates a dummy `ngc-secret` (`NGC_API_KEY`) that the
operator requires but the image ignores.

## Run standalone

```sh
docker build --build-arg GO_VERSION="$(awk '/^go /{print $2; exit}' hack/e2e/operators/nim/image/go.mod)" \
  -t nim-cpu:e2e hack/e2e/operators/nim/image
docker run -p 8000:8000 nim-cpu:e2e
curl http://localhost:8000/v1/health/ready
```

`NIM_REQUEST_LATENCY` (float seconds, default 0) adds a delay to
`/v1/chat/completions`, simulating processing time. `NIM_HTTP_PORT` overrides the
listen port (default 8000).

## Files

- `main.go` - the HTTP server (standard library only).
- `go.mod` - module definition; no third-party dependencies.
- `Dockerfile` - multi-stage build to a static binary on `distroless/static`.
