# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

# Build from the repository root so the `replace github.com/dsx-ai-factory/workload-map => ../`
# directive in operator/go.mod resolves against the local pkg/ tree. Prefer
# `make operator-image`, which supplies GO_LDFLAGS from the root Makefile.
# Keep GO_VERSION aligned with the `go` directive in operator/go.mod:
#
#   docker build \
#     --build-arg GO_VERSION=$(awk '/^go /{print $2}' operator/go.mod) \
#     -f operator/Dockerfile -t karta-operator:latest .
ARG GO_VERSION=1.26.3
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build

ARG TARGETOS
ARG TARGETARCH

ARG GO_LDFLAGS=""

WORKDIR /src
COPY . .
WORKDIR /src/operator
# See docs/FIPS.md for FIPS 140-3 support.
RUN CGO_ENABLED=0 GO111MODULE=on GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOFIPS140=v1.0.0 \
	go build -trimpath \
	-ldflags "${GO_LDFLAGS}" \
	-o /karta-operator ./cmd

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /karta-operator /karta-operator
USER 65532:65532
ENTRYPOINT ["/karta-operator"]
