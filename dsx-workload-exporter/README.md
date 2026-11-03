<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter

The exporter reports workload status and maps Pods to their workload, component,
instance and group. Prometheus can join these labels with CPU, memory and GPU
measurements. See the [metric definitions](../docs/dsx-workload-exporter/metrics.md)
and [query examples](../docs/dsx-workload-exporter/queries.md).

Metrics are served at `/metrics` on port 8080. Scrapes read saved results without
calling Kubernetes.

## Run

From the repository root:

```sh
make build-exporter
./bin/dsx-workload-exporter
```

- Uses kubeconfig or the in-cluster service account.
- Loads Karta definitions, then starts the controller.
- `/healthz` checks process health.
- `/readyz` requires healthy inventory and synchronized required watches.
- Stops cleanly on `Ctrl-C` or `SIGTERM`.

Use `--metrics-bind-address` to change the default `:8080` listener.

## Runtime configuration

Use the [Helm installation guide](../docs/dsx-workload-exporter/installation.md)
for a standalone catalog demo. The [configuration reference](../docs/dsx-workload-exporter/configuration.md)
lists supported Helm settings, binary flags, environment variables and defaults.

For the binary, put runtime settings at the YAML root without `exporter:`:

```sh
./bin/dsx-workload-exporter --config exporter.yaml
./bin/dsx-workload-exporter --help
```

## Controller flow

The [controller guide](pkg/controller/README.md) explains watches, queues, workers,
publication and cleanup.

| Package | Responsibility |
| --- | --- |
| [kartadefinitioninventory](pkg/kartadefinitioninventory/) | Load and select Karta definitions |
| [discovery](pkg/discovery/) | Follow object owners to a workload root |
| [labelevaluator](pkg/labelevaluator/) | Evaluate a label expression against one object |
| [labelresolver](pkg/labelresolver/) | Resolve metric labels from a workload and optional Pod |
| [controller](pkg/controller/) | Watch objects, process queued work and save results |
| [store](pkg/store/) | Hold results, source health and errors |
| [adapters/otel](pkg/adapters/otel/) | Convert saved results into metrics |

## Development

```sh
make exporter-generate-mocks
make check-exporter
make exporter-image IMAGE_TAG=dev
```

Regenerate mocks after changing interfaces. Controller integration tests use
an envtest Kubernetes API server and check store records and readiness. They do
not install operators or test the metrics collector.

Operator mapping gaps remain for Dynamo replica keys, Grove instance names,
JobSet group identity, RayJob submitter Pods and KServe status. Controller tests
do not establish complete operator coverage.
