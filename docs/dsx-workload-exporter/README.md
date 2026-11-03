<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter

The exporter maps each Pod to its workload, component, instance and group.
It reports workload status and creation time. Prometheus can join these identities
with CPU, memory and GPU measurements from other exporters.

- [Install with Helm](installation.md).
- [Metric names, labels and validity](metrics.md).
- [Configuration and defaults](configuration.md).
- [Build and run the binary](../../dsx-workload-exporter/README.md).

- [PromQL query examples](queries.md) for attribution and low observed GPU activity.

Low activity alone does not prove idleness.
