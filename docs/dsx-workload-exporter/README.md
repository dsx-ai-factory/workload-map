<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter

The exporter maps each Pod to its workload, component, instance and group.
It reports workload status and creation time. Prometheus can join these identities
with CPU, memory and GPU measurements from other exporters.

- [Install with Helm](installation.md).
- [Metric names, labels and validity](../keps/0001-metrics-exporter/README.md#metrics).
- [Configuration and defaults](configuration.md).
- [Build and run the binary](../../dsx-workload-exporter/README.md).

- [PromQL query examples](queries.md) for attribution, shared GPUs and low observed GPU activity.
- [Grafana dashboard and Prometheus rule examples](../../dsx-workload-exporter/examples/grafana/README.md).

Low activity alone does not prove idleness.
