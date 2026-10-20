<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# OpenTelemetry adapter

This adapter will turn published workload observations into OpenTelemetry metrics.
It will support the delivery modes defined by the exporter design: Prometheus
scraping or periodic OTLP export.

Collection will read calculated observations. Kubernetes reads, ownership walks,
Karta evaluation and submitter extraction belong outside this adapter.

This scaffold adds no OpenTelemetry dependency or metrics endpoint. Implementation
follows in a separate change.
