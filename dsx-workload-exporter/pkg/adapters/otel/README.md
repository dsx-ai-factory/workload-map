<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# OTel Collector

`Register` takes an OTel Meter, a Store `Capturer` and excluded statuses.
It returns a `metric.Registration`. It creates four observable gauges and one callback.
Each callback captures Store once and formats saved values, labels and health.

Status emits each enabled normalized phase as zero or one. The default excludes
`Suspending` and `Resuming`; see [configuration](../../../../docs/dsx-workload-exporter/configuration.md#status-phases).
A verified root with no selected Karta emits enabled phases as zero, with empty
`karta` and `karta_source` labels. Workload label rules still apply. Invalid retained Pod
mapping and status values carry `observation_valid="false"`. Invalid creation
timestamps are omitted. Collection does not read Kubernetes or run expressions.

The caller must unregister collection before shutting down the OTel provider.
The tests use a real provider, Prometheus reader and HTTP handler. Provider
settings, including the cardinality limit, are outside this formatter.

The application registers the real collector with a Prometheus reader and an
OTel MeterProvider. It disables the SDK cardinality limit so distinct Pod and
workload identities are not merged into an overflow row. Translation, scope
labels and target-info metrics are disabled to preserve the KEP names and labels.

The command serves `/metrics`, `/healthz` and `/readyz` on `:8080` by default.
Use `--metrics-bind-address` to choose another address. Readiness requires healthy
definition inventory and synchronized required Kubernetes watches. The
[controller](../../controller/README.md) resolves ownership, evaluates workloads
and writes the results that the collector reads from Store.

Overlapping `/metrics` requests are accepted. Each collection reads one Store
snapshot. The OTel SDK serializes collection callbacks; HTTP responses can be
encoded and written concurrently. Slow clients can keep multiple responses in
flight, increasing memory use. Health endpoints remain available.

On shutdown, the application stops workers and HTTP serving, drains requests,
unregisters collection, then shuts down the provider. Graceful HTTP and provider
shutdown each use a ten-second context. Runtime cancellation and callback
completion must cooperate; these contexts cannot interrupt a blocked callback.

PromQL join tests remain separate work.
