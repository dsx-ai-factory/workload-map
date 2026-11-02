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

The command watches Kubernetes objects, maintains Pod and workload records,
and serves the collector's Prometheus metrics at `/metrics` on port 8080.
Scraping reads the publication store without querying Kubernetes.

## Run

Run these commands from the repository root:

```sh
make build-exporter
./bin/karta-metrics-exporter --version
./bin/karta-metrics-exporter --help
./bin/karta-metrics-exporter
```

The command uses the current kubeconfig, or the in-cluster service account.
It starts the definition inventory and controller together, then runs until
Ctrl-C (`SIGINT`) or `SIGTERM`. `/healthz` reports process health. `/readyz`
requires healthy inventory and synchronized required watches. Shutdown stops
HTTP serving, joins the inventory and workers, then closes metric collection.
Use `--metrics-bind-address` to change the default `:8080` listener.

If the initial Karta LIST fails, discovery can still resolve ownership and
publish workload creation information. Pod results record the definition error
without claiming a complete mapping. Status and component mapping remain
unavailable and readiness stays false until the definition source recovers.

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
describes the overall deployment and metric contract. Enabling the exporter
creates its Deployment and a Service on port 8080, with health and readiness probes.

## Runtime configuration

For example, this file observes Pods in namespaces labeled `team: platform` and
uses the built-in Deployment definition:

```yaml
kartaDefinitionSource:
  catalog:
    enabled: true
    include: [apps-deployment-v1]
  cluster:
    enabled: false
namespaceSelector:
  matchLabels:
    team: platform
workers: 2
```

The runtime settings below use the same names under Helm's `exporter:` key.

| Setting | Default | Behavior |
| --- | --- | --- |
| `namespaceSelector` | Empty | Observe all namespaces; otherwise watch matching namespaces |
| `podSelector` | Empty | Publish matching Pods; other Pods remain available for owner traversal |
| `objectSelector` | Empty | Select workload objects by labels |
| `resourceRules`, `excludeResourceRules` | Empty | Select workload API groups, versions and resources; exclusions win |
| `ignoredOwnerKinds` | Empty | Stop discovery immediately before these owner kinds |
| `requiredKinds` | Empty | Require these resource sources to synchronize before readiness |
| `workers` | `2` | Default worker count for each Pod and workload queue |
| `leafWorkers`, `rootWorkers` | `0` | Override each queue's count; zero inherits `workers` |
| `requestTimeout` | `30s` | Bound resource discovery and each LIST page, not the lifetime of a watch |
| `apiQPS`, `apiBurst` | `20`, `40` | Share one Kubernetes client request limiter |

Kinds use slash strings, such as `apps/v1/Deployment` or `v1/Node`. Each resource
rule contains `apiGroups`, `apiVersions` and `resources` lists. An empty inclusion
list permits every resource. These filters select work; they do not grant RBAC.

Worker counts, request timeout and API limits also have kebab-case flags and
`KARTA_EXPORTER_*` environment variables, shown by `--help`. A flag overrides
the environment, which overrides the YAML file. Selectors, resource rules,
kinds and definition sources are configured in YAML only.

The chart grants LIST/WATCH access to Pods, Namespaces and the workload and
ancestor resources listed in `exporter.rbac.workloadRules`. Cluster Karta access
is conditional on the cluster definition source. Extend the workload rules for
custom owners. No wildcard resource or write permissions are granted by default.

## Controller flow

The [controller guide](pkg/controller/README.md) explains each of its six files,
the three queues, full-object and metadata watches, publication checks, and
cleanup. The definition inventory owns its Karta watch. The controller observes
inventory changes and owns workload watches. Pod and workload workers evaluate
the latest cached objects and write independent records to the upstream store.

`bootstrap.New` constructs these real components. `app.RunController` owns their
lifecycle without a collector. `app.Run` retains the collector registration
contract and also starts the inventory when it is supplied.

This change keeps the merged discovery, store, evaluator and catalog behavior.
Operator-specific mapping corrections remain separate, including Dynamo replica
keys, Grove instance names, JobSet group identity, RayJob submitter Pods and
KServe status. Controller tests do not establish complete operator coverage.

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

The controller integration tests use a real Kubernetes API server through
envtest. They inspect store records and readiness. They do not install operators
or test a metrics collector.
