# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

# Build from the repository root so the `replace github.com/dsx-ai-factory/workload-map => ../../../`
# directive in go.mod resolves. Keep GO_VERSION aligned with the `go` directive in
# go.mod by deriving it at build time:
#
#   GO_VERSION=$(awk '/^go /{print $2}' docs/examples/controller-runtime/go.mod)
#   docker build --build-arg GO_VERSION=$GO_VERSION \
#     -f docs/examples/controller-runtime/Dockerfile -t generic-controller-example:latest .
#
# The default below is a fallback for a plain `docker build`; the command above is
# the single source of truth.
ARG GO_VERSION=1.26.3
FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY . .
WORKDIR /src/docs/examples/controller-runtime
RUN CGO_ENABLED=0 GO111MODULE=on go build -trimpath -o /controller .

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /controller /controller
USER 65532:65532
ENTRYPOINT ["/controller"]
