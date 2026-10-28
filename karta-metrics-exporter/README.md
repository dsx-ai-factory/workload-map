<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta metrics exporter

Karta metrics exporter is a standalone component for connecting Kubernetes
workloads to resource-usage metrics. Its [metric contract](../docs/keps/0001-metrics-exporter/README.md#metrics)
describes workload status and the workload, component and group that each Pod
belongs to. Prometheus can join this identity with CPU and memory measurements
from cAdvisor and GPU measurements from DCGM.

For example, a Pod in JobSet `train` can belong to component `replicatedjob`,
instance `worker` and group `0`. The [query guide](../docs/karta-metrics-exporter/README.md)
shows how to group measurements by that identity.

The command validates label configuration. Kubernetes watches and metric
delivery are not implemented yet.

## Run

Run these commands from the repository root:

```sh
make build-exporter
./bin/karta-metrics-exporter --version
./bin/karta-metrics-exporter --help
./bin/karta-metrics-exporter
```

The process waits until Ctrl-C (`SIGINT`) or `SIGTERM` stops it.

To configure labels, create a YAML file using the
[label configuration example](pkg/labelevaluator/README.md#configuration).
Omit the `exporter:` wrapper when running outside Helm, then pass the file:

```sh
./bin/karta-metrics-exporter --config exporter.yaml
```

The label evaluator uses Common Expression Language (CEL) and is implemented.
The command validates `labelRules` and `genericLabelEvaluationLimits` and compiles
expressions at startup. Invalid rules or expressions stop startup with an error.
The label configuration reference lists the supported flags, environment
variables, defaults and precedence.

Build a container image with `make exporter-image IMAGE_TAG=dev`. The Helm chart
keeps the exporter disabled by default. The
[configuration reference](../docs/karta-metrics-exporter/configuration.md)
describes the proposed deployment, selection and metric delivery settings.

## Development

Component contracts live under `pkg/`. Each package documents its inputs,
outputs and implementation status.

| Package | Input and result |
| --- | --- |
| [kartadefinitioninventory](pkg/kartadefinitioninventory/) | Root GVK -> selected Karta and its source |
| [discovery](pkg/discovery/) | Object reference -> workload root and missing root coverage |
| [labelevaluator](pkg/labelevaluator/) | One object -> one configured label value |
| [labelresolver](pkg/labelresolver/) | Root, optional Pod and metric names -> labels for each metric |
| [controller](pkg/controller/) | Watches and queued work -> publications |
| [store](pkg/store/) | Publications -> consistent captured view |
| [adapters/otel](pkg/adapters/otel/) | Captured view -> OpenTelemetry observations |

Generated Mockery mocks live beside the interfaces they implement.

```sh
make exporter-generate-mocks
make check-exporter
```
