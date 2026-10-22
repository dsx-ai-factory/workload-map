# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Fictive CPU NIM stand-in: static Go binaries on a nonroot distroless base.
# The universal cache fixture skips manifest inspection in the pinned operator
# release, so neither the HTTP server nor the cache command needs a shell.
# Both binaries use the standard library only, with no module downloads.
ARG GO_VERSION
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod .
COPY main.go .
COPY cmd/create-model-store ./cmd/create-model-store
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /nim .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /create-model-store ./cmd/create-model-store

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /nim /nim
COPY --from=build /create-model-store /usr/local/bin/create-model-store
EXPOSE 8000
ENTRYPOINT ["/nim"]
