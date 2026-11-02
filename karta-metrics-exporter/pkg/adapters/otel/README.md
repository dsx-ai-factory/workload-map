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

The application registers the real collector with a Prometheus reader and an
OTel MeterProvider. It disables the SDK cardinality limit so distinct Pod and
workload identities are not merged into an overflow row. Translation, scope
labels and target-info metrics are disabled to preserve the KEP names and labels.

The command serves `/metrics`, `/healthz` and `/readyz` on `:8080` by default.
Use `--metrics-bind-address` to choose another address. Readiness requires the
runtime to implement `Ready() bool` and report that its Kubernetes sources are
initialized. The runtime scaffold remains mocked and unready. The definition
inventory, label resolver, Store and metrics/HTTP path are real; object reading,
ownership discovery and runtime workers remain mocked.

One scrape runs at a time. Overlapping `/metrics` requests return 503 while
health endpoints remain available.

On shutdown, the application stops workers and HTTP serving, drains requests,
unregisters collection, then shuts down the provider. Graceful HTTP and provider
shutdown each use a ten-second context. Runtime cancellation and callback
completion must cooperate; these contexts cannot interrupt a blocked callback.

`BenchmarkCollectIndependentResults` measures one real SDK collection for many
workloads and for one workload with 100,000 Pods. Identity and overflow checks
run outside the timed loop. Run it with:

```sh
go test -run '^$' -bench '^BenchmarkCollectIndependentResults$' -benchmem ./pkg/adapters/otel
```

Phase selection and PromQL join tests remain separate work.
