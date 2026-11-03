<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Output adapters

The [OpenTelemetry adapter](otel/README.md) translates saved workload observations
into four gauges. It does not discover workloads, evaluate Karta definitions or
run label expressions. The application wires it to a Prometheus reader;
native OTLP push is not configured by the current runtime.
