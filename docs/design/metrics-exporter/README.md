<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# DSX workload exporter: implementation design

A Pod can belong to a ReplicaSet while the workload a user monitors is its
Deployment. For `Pod api-1 -> ReplicaSet api-rs -> Deployment api`, discovery
follows controller owners to `api`. Runtime evaluates the Deployment's Karta and
saves Pod attribution and workload status separately. A scrape formats those
saved results without calling Kubernetes.

[KEP-0001](../../keps/0001-metrics-exporter/README.md) records the motivation.
The [metric reference](../../keps/0001-metrics-exporter/README.md#metrics) defines output.
The [configuration reference](../../dsx-workload-exporter/configuration.md)
owns settings, defaults, label examples and delivery limits.

## Component ownership

| Component | Responsibility |
| --- | --- |
| Application | Construct components, start runtime and serve HTTP. |
| Definition inventory | Load catalog/cluster definitions, select winners and report source health. |
| Controller runtime | Own watches, caches, workqueues, membership and publication checks. |
| Discovery | Follow controller owners through the runtime reader. |
| Label resolver and evaluator | Evaluate ordered rules against supplied full objects. |
| Store | Save independent Pod/workload results and capture result/health snapshots. |
| OTel adapter | Convert a captured Store view into four observable gauges. |

Startup validates configuration and compiles CEL before starting observation.
The application waits for initial inventory loading before starting runtime.
Shutdown cancels workers and watches, drains HTTP, unregisters collection and
closes the provider. See [application wiring](../../../dsx-workload-exporter/pkg/app/application.go).

## Resolve ownership before selecting a definition

Discovery follows controller owner references to the actual root. A Karta on an
intermediate ReplicaSet does not turn it into the root. Only an explicit ignored
GVK stops traversal early. The last verified object below that boundary becomes
the root with `owner_resolution="ignored_owner"`.

The reader resolves API scope and reads its cache. Missing ancestor coverage
starts shared metadata watches. Evaluated roots need full objects; a new full
watch must synchronize before replacing metadata coverage. Missing LIST/WATCH
permission is a failure, not a GET-only fallback.

UID replacement, stale input resource versions, malformed or multiple controller
owners, cycles and missing owners fail resolution without a partial workload.
Namespace and root selection do not authorize new roots by promoting excluded
objects' children. See [Discovery](../../../dsx-workload-exporter/pkg/discovery/README.md).

Inventory selects by root group/kind. Its selected API representation can differ
from the discovered version. Source failure and invalid selected definitions do
not mean absence. Confirmed absence permits generic identity, creation time and
zero-valued status phases. See [inventory selection](../../../dsx-workload-exporter/pkg/kartadefinitioninventory/README.md).

## Events and publication

The sources queue refreshes namespace scope and selected definitions. Pod jobs
resolve owners, update membership and evaluate Pod mappings/labels. Workload
object jobs discover roots; root-UID jobs evaluate status and creation metadata.
These are separate publications, not one atomic root-and-Pods transaction.

| Event | Result |
| --- | --- |
| Pod create/update | Queue that Pod; register its root and queue workload evaluation. |
| Root update | Queue its workload and descendant Pods. |
| Definition change | Refresh sources and requeue affected workloads/Pods. |
| Namespace labels change | Recompute selection and watched scopes. |
| Intermediate ownership change | Rescan affected namespace membership. |
| Pod deletion | Remove its saved Pod result. |
| Root deletion | Remove its workload result and requeue Pods; a live Pod can retain an invalid mapping. |
| Watch or evaluation failure | Keep available previous values and report invalidity/errors. |

Queues combine identical keys and serialize each key, while different keys can
run concurrently. A change accepted during evaluation causes publication checks
to reject stale results and retry. Object UIDs distinguish replacements with the
same name. API calls and evaluation happen outside the shared state mutex.
See the [controller guide](../../../dsx-workload-exporter/pkg/controller/README.md)
for handlers and queue ownership.

## Saved values and failures

Store clones mutable inputs on upsert. Returned record pointers are read-only by
convention. Capture copies containers and result pointers under one Store lock;
formatting happens after releasing it. Later replacements cannot mutate an
existing capture. Separate Pod and workload writes may appear at different
revisions in that capture.

Runtime, not Store, constructs replacements with retained successful values and
current errors. A label error can retain the whole previous metric result, even
when other label rules succeed. Status failure is separate from root metadata
failure, so it need not hide a verified creation timestamp.

Validity uses saved errors and captured dependency health. The current Store has
no TTL or captured clock. Source recovery can restore source-only validity before
reconciliation completes; it does not prove the calculation used recovered
inputs. Saved evaluation errors remain until replaced. These are implementation
limits, not a guarantee of continuously verified history.

Deletion removes current results, not backend history. An in-flight capture can
finish emitting a removed result. Runtime owns cleanup, reparenting and protection
against stale work restoring deleted UIDs. See the
[Store contract](../../../dsx-workload-exporter/pkg/store/README.md).

## Collection

The application wires an OTel Prometheus reader. Each collection captures Store
once. It emits dense status phases, saved Pod mappings and verified creation
metadata. Retained invalid mappings/status carry `observation_valid="false"`;
invalid creation metadata is omitted. Health uses bounded reasons, not raw error
strings. Workload failure rows are sparse.

Collection cannot discover owners or evaluate expressions. There is no native
OTLP push reader or configurable phase subset. A separate Collector may scrape
the endpoint. See the [adapter](../../../dsx-workload-exporter/pkg/adapters/otel/README.md)
for concurrency and shutdown behavior.

## Submitter capture

The API server's admission policy captures direct callers. It is implemented
separately from exporter reconciliation. Label rules read the resulting root
annotation; they do not infer a human identity behind a platform service account.
See the [admission design](submitter-capture.md) for rationale and alternatives.

## Verification boundaries

Package tests cover discovery, inventory, label evaluation, Store and collection.
Controller integration tests use envtest and assert saved records/readiness.
They do not establish complete operator mapping coverage or live Helm behavior.
The module [development notes](../../../dsx-workload-exporter/README.md#development)
list current mapping gaps. Run `make check-exporter` for exporter checks and
`make check` for repository presubmit. Documentation review is not test evidence.
