# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

# Build from the repository root so the SDK replace directive resolves locally.
# make exporter-image supplies the module's Go version and version linker flags.
ARG GO_VERSION=1.27.3
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build

ARG TARGETOS
ARG TARGETARCH
ARG GO_LDFLAGS=""
ENV GOTOOLCHAIN=local

WORKDIR /src
COPY . .
WORKDIR /src/dsx-workload-exporter
RUN CGO_ENABLED=0 GO111MODULE=on GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOFIPS140=v1.0.0 \
    go build -trimpath -ldflags "${GO_LDFLAGS}" -o /dsx-workload-exporter ./cmd

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /dsx-workload-exporter /dsx-workload-exporter
USER 65532:65532
ENTRYPOINT ["/dsx-workload-exporter"]
