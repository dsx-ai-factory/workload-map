<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta Metrics Exporter - High-Level Design

## Purpose

KEP-0001 (Karta metrics exporter) defines the contract: three gauge families, their
labels, the fault reasons, and the Helm values. This document describes how the
exporter is built to honor that contract: the process layout, the packages it reuses
or promotes from the CLI, the event flow, how observation health is derived, and what it costs
at cluster scale.

Where this design needs something the KEP does not yet say, it is listed under Open
questions rather than decided here.

## Scope

In scope:

- A standalone exporter binary and image, deployed by the existing `karta` chart.
- Promoting the definition resolver and the owner walk from `cli/pkg` to the
  library module, `karta/sdk/pkg/`, so
  the CLI and the exporter share one implementation.
- The in-memory snapshot, its update path, and the OTel/Prometheus read path.
- The health tracker that drives `karta_exporter_observation_healthy` from informer
  and reconcile signals.
- Chart, RBAC, and test strategy.

Out of scope:

- Native OTLP push. The OTel SDK is used from day one so it stays a reader swap,
  but the push readers, their config, and stale-series handling are deferred.
- Recording rules, alerts, dashboards. The KEP keeps them with the platform.
- Measuring usage. CPU, memory, and GPU come from cAdvisor and DCGM.
- Writing to workloads. The exporter is read-only.

## Architecture overview

```text
                     +-------------------------- exporter process ---------------------------+
                     |                                                                       |
 Karta CRs --watch-->| Definition registry --(selected GVKs)--> Informer manager              |
 catalog (embedded)->|        |                                   |  roots, owners, Pods     |
                     |        v                                   v                          |
                     |  definition per GK          event handlers -> root workqueue          |
                     |        |                                   |                          |
                     |        +--------------> Root reconciler <--+--watch errors, sync,     |
                     |                         | owner resolver       |  progress            |
                     |                         | status, attribution  v                      |
                     |                         |   failures ------> Health tracker           |
                     |                         v                     |                       |
                     |                     Snapshot store <----------+                       |
                     |                         |                                             |
                     |              OTel observable gauges (read on collect)                 |
                     |                         |                                             |
                     |              OTel Prometheus reader -> GET /metrics                   |
                     +-------------------------|---------------------------------------------+
                                               v
                                          Prometheus
```

Two loops feed the snapshot, and a tracker derives health from both:

1. Watch loop. Informers keep a local cache of selected root types, intermediate
   owners, and Pods. Every event maps to one or more root keys on a workqueue.
2. Reconcile loop. Workers pop a root key, recompute that root's status rows and
   the attribution rows of its Pods, and replace them in the snapshot.
3. Health tracker. Collects watch errors, sync state, and watch progress from the
   informers, and failures from the reconciler, and turns them into health rows.
   It makes no API calls of its own.

Collection never computes. A scrape reads the latest snapshot under a read lock, so
scrape latency does not depend on cluster size beyond serialization.

## Components

### Process and module layout

The exporter is a new Go module at `exporter/`, mirroring `operator/`: it requires
the library module `karta/sdk` at `v0.0.0` with a relative `replace`, so it builds
against the library in the same checkout. It builds its own image
(`karta-exporter`) and is versioned with the chart, like the operator.

```text
exporter/
  cmd/main.go              flags, manager setup, signal handling
  pkg/registry/            definition selection and watch
  pkg/informers/           dynamic informer lifecycle per selected GVK
  pkg/reconcile/           root reconciler and workqueue wiring
  pkg/snapshot/            the store and its row types
  pkg/health/              health tracker and health rows
  pkg/metrics/             OTel meter, observable gauges, HTTP server
```

The exporter runs on controller-runtime's manager for the cache, health probes, and
graceful shutdown, but it does not use the reconciler-per-GVK pattern. The set of
watched GVKs changes at runtime as definitions appear, which controller-runtime
controllers do not support well; a single workqueue keyed by root identity does.

The exporter runs as its own binary, not inside the operator, so a failure in one
does not stop the other. The operator serves the Karta admission webhooks with
`failurePolicy: Fail`: while it is down, the API server rejects every create and
update of a Karta definition. Workloads are not affected, since the webhooks match
only Karta resources. The exporter caches every watched workload and Pod, so its
memory grows with the cluster, and a Pod burst can get it OOM-killed. In a shared
process, that would also block Karta writes until restart; apart, it only leaves a
gap in metrics.

The split also keeps permissions and sizing apart. The exporter needs cluster-wide
read access to every workload kind and its intermediate owners, which the operator
does not, so a merged process would grant the webhook more than it uses. Each binary
gets resource limits sized to its own load, and the exporter can be scaled,
restarted, upgraded, or disabled without touching admission. The cost is one more
image and Deployment to build and ship.

### Shared library promotion

Two pieces live in the CLI module today and must move to the library module
(`karta/sdk/pkg/`) so the exporter can import them. Both moves are `git mv` plus API
widening; the CLI keeps its behavior apart from the owner walk bound described
below.

| Today | Moves to | Change |
| --- | --- | --- |
| `cli/pkg/definitions` resolver (catalog + cluster, precedence, collisions) | `karta/sdk/pkg/definitions` | Accept incremental updates from a watch, not only a one-shot `Load`. Expose the tie-break (oldest creation time, then name) as part of the contract. |
| `cli/pkg/workload/pods.go` `PodAttributor` (top-down: keep Pods that reach a known root UID) | `karta/sdk/pkg/ownership` | Add a bottom-up `Resolve(pod) -> Root` used by the exporter. Both directions share one owner getter, one depth bound, and one boundary rule. |

`ownership` returns a typed outcome so the caller maps it straight to labels and
reasons:

```go
type Outcome string

const (
    OutcomeResolved     Outcome = "resolved"      // walk ended at the top described owner
    OutcomeIgnoredOwner Outcome = "ignored_owner" // walk stopped before an ignoredOwnerKinds GVK
)

// ErrUnresolvedOwner is returned for a missing owner or a UID mismatch below the
// boundary. Read failures are returned wrapped, unchanged, so the caller can tell
// permission_denied from api_unavailable with apierrors.
var ErrUnresolvedOwner = errors.New("unresolved owner")

type Root struct {
    Object  *metav1.PartialObjectMetadata
    GVK     schema.GroupVersionKind
    Outcome Outcome
}

type Resolver interface {
    Resolve(ctx context.Context, obj metav1.Object) (*Root, error)
}
```

`Resolve` follows controller references only, stays in the object's namespace,
stops before any GVK in `ignoredOwnerKinds`, and selects the highest owner that has
a selected definition. The owner getter is an interface, so the CLI backs it with
direct dynamic reads and the exporter backs it with its informer cache.

The walk is bounded at 8 hops by default, matching the KEP, and the bound is
configurable: `--max-owner-depth` on the CLI and `exporter.maxOwnerDepth` in the
Helm values. The bound is what ends a cycle in malformed owner data; real chains are
two to four hops, and nested platforms such as Dynamo on Grove sit at the deep end.
The CLI moves from its current 6 to the same default when it switches to
`karta/sdk/pkg/ownership`.

A walk that reaches the bound while the chain still continues returns
`ErrUnresolvedOwner`, even if a described owner was passed on the way. The highest
described owner cannot be known without the rest of the chain, and the KEP prefers
no row to an unconfirmed root.

### Definition registry

The registry owns one decision: which definition describes each group and kind.

- Inputs: the embedded catalog (`karta/sdk/pkg/catalog`) when `useCatalog` is true, and Karta
  CRs from a watch.
- Selection: one definition per group and kind; cluster beats catalog; ties use
  oldest creation time, then name. The definition's root GVK fixes the version the
  exporter observes, so other served versions never produce duplicate rows.
- Output: the selected set, with the definition name and source for the `karta`
  and `karta_source` labels.

A selection change must not leave rows behind for a kind, a definition, or a root
that no longer applies. The registry handles a change in this order:

1. Diff the old and new selection.
2. For each deselected group and kind, purge every row of that kind from the
   snapshot (status, Pod attribution, and workload health) in one step, then stop
   its informer. Purging first matters: once the informer is stopped, the reconciler
   can no longer list those roots to find their rows.
3. Start informers for newly selected GVKs and wait for them to sync.
4. Run a full resync: re-resolve every Pod and reconcile every root, across all
   kinds, not only the changed one. A change to one kind can move roots in another.
   Removing the JobSet definition while Job keeps one turns Jobs that were
   intermediate owners into roots, and their Pods must move to them; adding a
   definition above existing roots does the reverse. Selection changes are rare
   (a definition added, edited, or removed), so a full pass is affordable, and it is
   the only simple rule that gets the cross-kind case right.

An invalid definition stays selected and fails, rather than being skipped. An
invalid cluster definition still takes precedence over the catalog for its kind, so
a broken override is visible instead of silently masked by a catalog fallback with
different semantics. Its informer keeps running, so every workload of the kind is
known: each gets health `0` with `invalid_definition`, and the kind emits no status
or Pod attribution rows, including rows left from the previous valid version. This
matches the KEP's rule for failed status evaluation. Invalid definitions should be
rare, since the operator's webhook validates Karta resources on admission; this
covers what admission cannot catch, such as JQ expressions that fail only at
evaluation time.

### Informer manager

The cache is the exporter's dominant memory cost, so every informer is sized to what
the reconciler reads.

| Watched | How | Why |
| --- | --- | --- |
| Selected root GVKs | Full dynamic informer, filtered by `namespace` | Status and attribution expressions read spec and status. |
| Intermediate owner GVKs (ReplicaSet, Job, LWS, ...) | Metadata-only informer | The walk reads only owner references and UIDs. |
| Pods | Typed informer with a transform | Trim to metadata, labels, annotations, owner references, and the fields that definition selectors read. Drop `managedFields`. |
| Karta CRs | Typed informer | Registry input. |
| CustomResourceDefinitions | Typed informer, trimmed to group, kind, and served versions, as the operator's `TrimCRDFields` does | Detect a root API being installed, removed, or changing its served versions, for `requiredKinds` and for starting informers without a restart. |

Intermediate owner GVKs are not known up front. The manager learns them from
owner references it meets during walks and starts a metadata informer the first time
a GVK appears. Until that informer syncs, walks through it are retried, not failed.
A GVK the exporter cannot list (RBAC, or the CRD is gone) produces resource-type
health `0` and is retried with backoff.

Every informer registers a watch error handler and requests watch bookmarks, which
the health tracker consumes (see Health tracker below).

#### Missing APIs and `requiredKinds`

With `useCatalog: true` the exporter knows dozens of kinds, and a typical cluster
runs only a few of their operators. A selected root GVK whose CRD is not installed
is therefore skipped without a health row; otherwise every cluster would report
permanent failures for operators it never meant to run.

Skipping makes a missing API look the same as an installed kind with no workloads:
neither produces status or health rows. A consumer cannot tell "no JobSets are
running" from "the exporter cannot see JobSets", which happens when an operator is
uninstalled, a CRD is renamed in an upgrade, or the version a definition watches
stops being served.

`requiredKinds` (a KEP Helm value, default empty) removes that ambiguity for the
kinds a platform depends on. Each entry is an exact GVK, such as
`jobset.x-k8s.io/v1alpha2/JobSet`. For a listed kind, a missing API produces a
resource-type health row with value `0` and `reason="api_not_found"` instead of
nothing.

| Situation | Kind not in `requiredKinds` | Kind in `requiredKinds` |
| --- | --- | --- |
| CRD not installed, or version not served | Skipped, no rows | Health `0`, `api_not_found` |
| Installed, exporter cannot list it (RBAC) | Health `0`, `permission_denied` | Health `0`, `permission_denied` |
| Installed and readable | Health `1`, `none` | Health `1`, `none` |

Permission errors always report, because an installed API the exporter cannot read
is a real gap. `requiredKinds` changes only the not-installed case. The informer
manager watches CRDs, so a required kind that appears later starts its informer and
recovers without a restart.

`api_not_found` is a new reason, separate from `api_unavailable`. The two call
for different actions: `api_not_found` means the API is not installed or the
version is not served, and someone must install or upgrade an operator;
`api_unavailable` means the API server failed to answer, which is usually transient.

Matching is on the exact version. An upgrade that stops serving the listed version
reports `api_not_found` even if the operator now serves a newer one, because queries
and dashboards written against the listed version may no longer hold. Moving to the
new version is an explicit change to `requiredKinds`.

Every `requiredKinds` entry is also checked against the selected definitions, once
the Karta informer has synced and the registry has made its first selection, and
again on every selection change:

- No selected definition for the entry's group and kind: the exporter keeps
  running, logs a warning naming the entry, and reports resource-type health `0` for
  it with `reason="no_definition"`. A required kind that no Karta definition
  describes cannot be observed, and the health row makes that visible to the same
  alerts that watch every other failure.
- A selected definition for the group and kind, but at a different version: the
  exporter logs a warning naming both versions and continues. Health for the entry
  still follows the listed version.

The behavior is the same whether the definition is missing at startup or is removed
while the exporter runs, when a cluster Karta is deleted and no catalog definition
replaces it. In the runtime case the kind's rows are also purged, as for any
deselected kind. The exporter never exits over `requiredKinds`: exiting would drop
metrics for every kind and crash-loop on restart, and the health row already
reports the problem. A definition added later clears the row on the next selection
change, without a restart.

The check needs no cluster API beyond the Karta CRs and the embedded catalog, so it
does not depend on the listed CRDs being installed yet.

### Root reconciler

Every event maps to root keys `(namespace, group, kind, name, uid)` on a
rate-limited workqueue. Pod and owner events are mapped by resolving the root
through the cache; a root event maps to itself. Deduplication in the queue collapses
bursts, for example a JobSet rollout that touches hundreds of Pods.

For one root key, a worker:

1. Reads the root from the cache. If gone, deletes its rows and stops.
2. Builds the component factory from the selected definition and evaluates status
   through `karta/sdk/pkg/tree`. Matched phases become `1`, the rest `0`. A definition with no
   status emits no status rows. An evaluation error emits no status rows and sets
   workload health to `evaluation_failed`.
3. Lists Pods attributed to this root from a cache index (index by resolved root
   UID, maintained on Pod events). For each Pod, places it with the definition's
   selectors through `PodQuerier`: `MatchesComponentType`, `ExtractInstanceId`,
   `ExtractReplicaKey`. Unmatched component or instance becomes `<unknown>`. A
   selector that declares a group key but yields none is an attribution failure:
   the row is dropped and workload health is `0`.
4. Replaces the root's rows in the snapshot as a single unit.

Status and attribution are computed together per root so a scrape never sees a
status row for a definition that the attribution rows no longer use.

Bare Pods, and Pods whose walk ends without a described owner, are not queued and
produce no rows. A walk that fails below the boundary produces no rows and marks
the namespace as `unresolved_owner` for known workloads, as the KEP specifies.

### Snapshot store

```go
type Snapshot struct {
    mu         sync.RWMutex
    generation uint64                                    // bumped at the start of each full resync
    roots      map[types.UID]RootRows                    // status rows and owned pod rows, by root UID
    byKind     map[schema.GroupKind]sets.Set[types.UID]  // roots per kind, for purging a deselected kind
    health     map[HealthKey]HealthRow                   // resource_type and workload scope
}
```

Rows are stored with their final label values, so collection is a copy, not a
computation. Each root replaces its own entry; a version change of the selected
definition replaces labels while the UID stays the same, which is what keeps
Prometheus history joinable across versions.

Rows leave the snapshot through three paths. Removing a row from the snapshot is
enough: the row disappears from the next scrape and Prometheus marks the series
stale.

- Per root: the reconciler finds the root gone from the cache and deletes its
  entry.
- Per kind: the registry purges a deselected kind through `byKind` before stopping
  its informer.
- Sweep: every row carries the generation of the pass that wrote it. When a full
  resync finishes, rows with an older generation are deleted. This is the backstop
  for any deletion a specific path missed, so a bug in one path produces a delayed
  removal, not a permanent stale row.

Full resyncs run on every selection change and on a periodic timer (default 10
minutes), so the sweep bounds how long any stale row can survive.

### Metrics surface

The exporter uses the OTel Go SDK with the Prometheus reader. Each family is an
`Int64ObservableGauge` whose callback walks the snapshot under the read lock. The
reader serves `/metrics` on the configured port next to `/healthz` and `/readyz`.

Using OTel rather than `client_golang` collectors directly costs one dependency but
makes native OTLP a reader swap: the callbacks, labels, and snapshot do not change.
Reader settings drop OTel's default `otel_scope_*` labels and unit suffixes so the
exposed names match the KEP exactly; a golden-file test locks this.

`/readyz` turns ready only after the registry has selected definitions, every
informer has synced once, and the first reconcile of every queued root has
finished. Before that, a scrape would report missing health as absent, which the
KEP defines as unknown, not healthy, so readiness gating avoids a misleading first
sample.

### Health tracker

Health is derived from signals the exporter already has. There is no second read
path: a direct API reader would duplicate what the informers report, add API load,
and create a second view of the same object that can briefly disagree with the
cache and make health flap.

Resource-type health, per watched GVK and namespace scope:

| Signal | Source | Result |
| --- | --- | --- |
| List or watch returns 401 or 403 | Informer watch error handler | `permission_denied` |
| List or watch returns 404, or the CRD informer shows the GVK is not served | Informer watch error handler, CRD informer | `api_not_found` (reported only for `requiredKinds`; other kinds are skipped) |
| A `requiredKinds` entry has no selected definition, at startup or later | Registry | `no_definition` |
| List or watch returns 5xx, a timeout, or a connection error | Informer watch error handler | `api_unavailable` |
| Informer not yet synced | `HasSynced` | `0`, held until sync |
| No event and no bookmark within the staleness window | Last-progress timestamp per informer | `0`, staleness reason |
| Synced, no error since the last success, progress within the window | All of the above | `1`, `none` |

Workload health combines the resource-type health of every GVK the workload
depends on (its root type and each intermediate owner type on its Pods' walks)
with the reconciler's last result for that root: `unresolved_owner`,
`evaluation_failed`, or `invalid_definition` from the registry. A dependency
failure is reported on the affected workload with the dependency's reason, as the
KEP requires.

Watch bookmarks are what make staleness detectable without extra reads. The API
server sends a bookmark periodically on an idle watch, so a watch that is open but
delivers nothing for longer than the window (default 2 minutes) is treated as stuck.
The tracker drops health to `0` and restarts that informer, which re-lists.

Missed events need no handling here: a watch that falls behind gets 410 Gone and
the informer re-lists, which surfaces as an error and then a fresh sync.

Revoked permissions are detected when the watch reconnects, since an open watch
keeps streaming after RBAC changes. The informer watch timeout is set to 5 minutes,
which bounds detection to that window. A shorter bound would cost more reconnects
than it is worth for this signal.

Recovery needs no special path. The next successful list, watch event, or bookmark
on the failed GVK, together with a clean reconcile of the root, replaces the
failure row with `reason="none"`.

## Event flow examples

New JobSet Pod:

```text
Pod add -> resolve via cache: Pod -> Job (metadata) -> JobSet (selected root)
        -> enqueue (team-a, jobset.x-k8s.io, JobSet, train, w-j)
        -> reconcile: status from JobSet, place Pod: replicatedjob / worker / 0
        -> snapshot.roots[w-j] replaced
next scrape -> karta_pod_workload_info{..., pod="train-worker-0", replica_group_id="0"} 1
```

Cluster definition overrides the catalog:

```text
Karta CR add -> registry selects cluster definition for jobset.x-k8s.io/JobSet
             -> informers unchanged (same root GVK), full resync
             -> status rows replaced with karta="<cr-name>", karta_source="cluster"
             -> sweep deletes nothing: every JobSet row was rewritten
```

Definition removed, child kind becomes the root:

```text
JobSet Karta CR delete, no catalog fallback -> JobSet deselected
  -> purge every JobSet row (status, Pod attribution, health), stop JobSet informer
  -> full resync: Pod -> Job, walk now ends at Job (selected root)
  -> Job status rows appear; Pods re-attributed to their Jobs
```

RBAC revoked on Jobs:

```text
Job watch reconnects (within the 5m watch timeout) -> re-list returns 403
watch error handler -> health tracker
  -> resource_type health {kind="Job", reason="permission_denied"} 0
  -> workload health for affected JobSets: permission_denied 0
  -> attribution rows for those Pods withdrawn, no unconfirmed root published
```

## Scale and cost

Series count per scrape, before Prometheus deduplicates across replicas:

| Family | Series |
| --- | --- |
| `karta_pod_workload_info` | one per attributed Pod |
| `karta_workload_status` | nine per root with status (one per phase) |
| `karta_exporter_observation_healthy` | one per root, plus one per type and namespace scope |

A cluster with 5,000 workloads and 40,000 Pods exposes about 40,000 + 45,000 +
5,000 = 90,000 series. `metrics.workloadPhases` narrows the phase set and is the
main lever if this is too many.

`pod_uid` and `workload_uid` make every series unique per lifetime, which is
intentional (names are reused) and is the same cardinality profile as
kube-state-metrics `kube_pod_info`. Churn, not size, is the cost to watch for
short-lived batch Pods.

Memory is dominated by the Pod cache. With the transform above, the target is the
same order as kube-state-metrics on the same cluster. The e2e scale test measures it.

## Deployment

The exporter is a new optional block in the existing `karta` chart, matching the
KEP's `exporter.*` values. With `exporter.enabled: false` (the default) nothing
is rendered.

Rendered objects: Deployment, Service, ServiceAccount, an aggregated read-only
ClusterRole with a binding, and optionally ServiceMonitor and NetworkPolicy.

RBAC:

- Built-in rules: `get`, `list`, `watch` on Pods and Karta CRs.
- `exporter.rbac.workloadRules`: the root and intermediate kinds. The default list
  covers the catalog's root kinds and their common intermediates (ReplicaSet, Job,
  StatefulSet, LeaderWorkerSet).
- The ClusterRole aggregates any ClusterRole labeled
  `karta.run.ai/exporter-rbac: "true"`, so platforms can add kinds without forking.

Replicas: each replica is independent and complete; there is no leader election.
Prometheus scrapes each endpoint, and the KEP's deduplication query merges identical
rows and rejects conflicting ones. Running two replicas is how a rollout avoids a gap.

The metrics endpoint has no TLS or auth in the first release. The chart ships a
NetworkPolicy that admits only the monitoring namespace when enabled; kube-rbac-proxy
is the follow-up if direct auth is required.

## Test plan

| Level | What it proves |
| --- | --- |
| Unit, `ownership` | Bottom-up and top-down walks agree; default and configured depth bound, including a chain cut off at the bound; ignored boundary; UID mismatch; read-error classification. |
| Unit, `definitions` | Precedence, tie-break, and incremental update produce the same result as a fresh load. |
| Unit, reconciler | Per catalog definition, recorded workload and Pod fixtures produce the expected rows. Reuses the catalog e2e recordings so every catalog entry is covered. |
| Unit, `requiredKinds` check | An entry with no selected definition reports `no_definition` and the exporter keeps running, both at startup and after the definition is deleted; adding the definition clears the row; a version mismatch warns and continues; the check passes when the listed CRDs are not installed yet. |
| Golden, `/metrics` | Exact names, labels, and absence of OTel scope labels. |
| envtest integration | Definition add, override, and delete, with no rows left from the previous selection; a deselected parent kind re-attributes Pods to the child kind; an invalid override drops status and Pod rows and reports `invalid_definition`; root and Pod delete; RBAC denial through an impersonated client; recovery replaces the failure row; a required kind whose CRD is absent reports `api_not_found`, and a 503 reports `api_unavailable`; a watch that stops sending events and bookmarks goes stale and is restarted. |
| e2e, kind | Chart install, ServiceMonitor scrape by a real Prometheus, the KEP's dedup and CPU queries return expected rows. |
| Scale | Synthetic 5,000 roots and 40,000 Pods: memory, reconcile lag after a burst, scrape latency. |

## Rollout

1. Promote `definitions` and `ownership` to `karta/sdk/pkg/`; switch the CLI to them,
   separately reviewable. The only behavior change is the owner walk bound: the
   default rises from 6 to 8 and becomes configurable with `--max-owner-depth`.
2. Exporter with attribution and status rows, Prometheus pull, chart disabled by
   default.
3. Health tracker and health rows; readiness gating.
4. Scale test results published; default RBAC rules finalized.
5. Native OTLP, as a KEP amendment.

The KEP update that accompanies step 2 adds what this design decided beyond the
current KEP: the `maxOwnerDepth` Helm value with default `8`, the `api_not_found`
and `no_definition` reasons, and exact-version matching for `requiredKinds`.

## Open questions

1. Health wording and staleness reason. The KEP says health is `1` "after fresh
   Kubernetes reads verify the observation". This design derives health from
   informer state rather than separate reads. Proposed KEP wording: health is `1`
   when every watch the workload depends on is synced, has had no error since its
   last success, and has shown progress (an event or a bookmark) within the
   staleness window, and the last reconcile of the workload succeeded. The KEP also
   needs a reason for a stuck watch; proposal: `watch_stale`. If maintainers want an
   independent check of the cache, it can be an optional low-frequency audit, off by
   default.
2. Namespace scope and RBAC: the KEP notes that namespace filtering does not narrow
   authorization. Should the chart offer a namespaced Role mode when `namespace` is
   set?
3. Unknown-owner blast radius. The KEP marks every known workload in a namespace
   unhealthy when one Pod's owner walk fails, because the Pod's missing usage could
   make any of them look idle. In a namespace shared by many workloads, one broken
   Pod pauses decisions for all of them until it is fixed. The owner reference at
   the failed hop still names the kind, name, and UID it pointed to, which often
   identifies the affected workload. Proposal, which needs a KEP change:
   - The failed reference points to a kind with a selected definition: mark only
     that workload unhealthy.
   - It points to any other kind: keep the namespace-wide rule, since what sits
     above the missing object is unknown.
   - In both cases, report `unresolved_owner` only after the walk keeps failing for
     a grace period (default 30s). Informers for different kinds sync independently,
     so a new Pod can be seen before its Job; without the grace period, normal
     workload creation would briefly mark the namespace unhealthy.
4. Module placement: a new `exporter/` module, or a subcommand of the
   operator binary to share an image. This design proposes a separate module for
   failure isolation.
