<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta metrics exporter

This module is the starting point for the metrics exporter. The command logs its
version and waits for SIGINT or SIGTERM. It does not watch Kubernetes or expose
metrics yet.

| Package | Responsibility | Included here |
| --- | --- | --- |
| [submitterextractor](pkg/submitterextractor/) | Extract the submitter from object metadata. | Interface |
| [kartadefinitioninventory](pkg/kartadefinitioninventory/) | Select a Karta definition and retain its source. | Interface |
| [discovery](pkg/discovery/) | Resolve ownership and interpret workloads with Karta. | README |
| [controller](pkg/controller/) | Connect watches, work queues and calculations. | README |
| [adapters/otel](pkg/adapters/otel/) | Export calculated observations through OpenTelemetry. | README |

Build and run from the repository root:

```sh
make build-exporter
./bin/karta-metrics-exporter --version
./bin/karta-metrics-exporter
```

Build the image with `make exporter-image IMAGE_TAG=dev`. The chart keeps the
exporter disabled by default. To render it with an image built from this branch:

```sh
helm template karta ./charts/karta \
  --set exporter.enabled=true \
  --set exporter.image.repository=example.com/karta-metrics-exporter \
  --set exporter.image.tag=dev
```

Push or load that image into the target cluster before installing it. This
scaffold does not add a release publishing workflow.
