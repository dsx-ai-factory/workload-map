<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# OTel Collector

`Register` takes an OTel Meter and a Store `Capturer` and returns a
`metric.Registration`. It creates four observable gauges and one callback.
Each callback captures Store once and formats saved values, labels and health.

Status emits all nine normalized phases as zeros or ones. Invalid retained Pod
mapping and status values carry `observation_valid="false"`. Invalid creation
timestamps are omitted. Collection does not read Kubernetes or run expressions.

The caller must unregister collection before shutting down the OTel provider.
The tests use a real provider, Prometheus reader and HTTP handler. Provider
settings, including the cardinality limit, are outside this formatter.

The existing `Collector` interface and its generated mock remain for the startup
scaffold. The command still uses that scaffold. Phase selection, command HTTP
wiring and provider shutdown are separate work from this formatter baseline.
