<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter

The exporter reports workload status and maps Pods to their workload, component,
instance and group. Prometheus can join these labels with CPU, memory and GPU
measurements. See the [metric definitions](../docs/keps/0001-metrics-exporter/README.md#metrics)
and [query examples](../docs/dsx-workload-exporter/README.md).

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

For Helm, set `exporter.enabled: true`. Settings and defaults are under `exporter:`
in [charts/karta/values.yaml](../charts/karta/values.yaml).

For the standalone binary, put runtime settings at the YAML root, without the
`exporter:` wrapper. Omit chart-only settings such as `enabled`, `image` and `rbac`.

```sh
./bin/dsx-workload-exporter --config exporter.yaml
./bin/dsx-workload-exporter --help
```

Selectors, resource rules, kinds and definition sources use YAML. Worker counts,
request timeout and API limits also have flags and environment variables listed by
`--help`. For those settings, flags override environment variables, which override
YAML. See the [label configuration example](pkg/labelevaluator/README.md#configuration)
for custom labels and expression limits.

Namespace selection does not restrict cluster-scoped resources or RBAC.
The chart's default `exporter.rbac.workloadRules` grants LIST/WATCH access to all
resources, including Secrets. Replace it with explicit resources to limit access.

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
