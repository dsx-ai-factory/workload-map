# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Fictive CPU NIM stand-in: a static Go binary on a distroless base. The final
# image has no shell, package manager, Python runtime, or third-party packages,
# so it carries effectively no CVEs. The build uses the standard library only,
# so it needs no module downloads.
FROM golang:1.26.8-bookworm AS build
WORKDIR /src
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /nim .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /nim /nim
EXPOSE 8000
ENTRYPOINT ["/nim"]
