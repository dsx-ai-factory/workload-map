<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Controller

The controller turns Kubernetes object changes into Pod and workload results in
[the publication store](../store/README.md). A Pod update must refresh its owner
mapping and labels. A workload update must refresh its status and affected Pods.
A Karta definition change must trigger reevaluation even when those objects stay
unchanged.

For example, this Pod belongs to ReplicaSet `web-rs`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: web-7
  namespace: default
  uid: pod-7
  ownerReferences:
    - apiVersion: apps/v1
      kind: ReplicaSet
      name: web-rs
      uid: replica-set-1
      controller: true
```

If `web-rs` belongs to Deployment `web`, discovery follows
`Pod web-7 -> ReplicaSet web-rs -> Deployment web`. The Pod result records
`pod-7 -> web`. A separate workload result records the Deployment's creation
information and its status from the selected Karta definition.

The input Pod is the leaf. The object returned by discovery is the root.
Discovery stops at an object with no controller owner, or immediately before an
owner kind listed in `IgnoredOwnerKinds`.

## The six files

| File | Responsibility |
| --- | --- |
| [runtime.go](runtime.go) | Construct the controller, start workers, observe definition changes, report readiness and stop work |
| [reader.go](reader.go) | Expose accepted cached objects to discovery and resolve Kubernetes resource mappings |
| [sources.go](sources.go) | Own shared LIST/WATCH sources, accept snapshots and route object changes to queues |
| [state.go](state.go) | Own cached objects, Pod/root membership and publication checks |
| [leaf.go](leaf.go) | Discover one Pod's root, evaluate component mapping and labels, and publish its result |
| [root.go](root.go) | Discover selected objects, evaluate known roots, and publish workload results |

## Runtime and queues

`NewReader` creates an empty cache. `New` connects that reader to the controller
and allocates its queues. Construction does not start watches. The caller starts
`Runtime.Run` and the definition inventory's lifecycle with a shared cancellation
context. Inventory owns the Karta watch; Runtime consumes `List()` snapshots and
`Changes()` notifications.

`Runtime.Run` starts these workers:

| Queue | Input | Work |
| --- | --- | --- |
| `exporter-sources` | The string `"sources"` | Refresh definitions and namespace scopes, then start shared resource watches |
| `exporter-pods` | Pod identity | Read the latest Pod and reconcile its mapping |
| `exporter-pods` | Namespace scope with `Rescan: true` | Requeue cached Pods and selected objects in that scope |
| `exporter-workloads` | Selected object identity | Discover its root and register membership |
| `exporter-workloads` | Root UID | Evaluate the root's status and labels |

Object identities contain group, version, kind, namespace, name and UID. A UID
identifies an object independently of its name. Queue keys do not contain object
bodies or resource versions. Workers read current objects when processing keys.
A scope rescan reads the cache and makes no API request.

There is one sources worker. `Workers` defaults to two. `LeafWorkers` and
`RootWorkers` inherit that value unless explicitly set. `ReaderOptions.RequestTimeout`
defaults to 30 seconds. Empty selectors match everything. Without a namespace
selector, namespaced watches cover all namespaces. Otherwise, the Namespace
cache determines the admitted scopes. Cluster-scoped watches use an empty scope.
Resource exclusions take precedence over resource inclusions.

`Ready()` requires a started, uncanceled runtime, healthy definition inventory,
and synchronized required sources. Pods are always required. Namespaces are
required when namespace selection is enabled. `RequiredKinds` adds other kinds.
A required source awaiting full-object replacement is not ready.

On cancellation, Runtime shuts down all three queues, cancels active and
replacement watches, and waits for workers and watch goroutines to stop.

## Reader and sources

The reader's object cache contains accepted Kubernetes objects and source health.
The publication store contains calculated results. Discovery and workers read the
object cache; callers use `Store.Capture()` to inspect published results.

A client-go Reflector maintains each shared resource collection and namespace
scope. One watch can serve all Deployments in that scope. `LIST` supplies a
complete snapshot. `WATCH` supplies later additions, updates and deletions.
`reflectorStore.Replace` validates the whole snapshot before accepting absence as
deletion. `applyWatchEvent` accepts individual events. Both update the cache and
queue identities without evaluating expressions.

| Objects | Cached data | Purpose |
| --- | --- | --- |
| Pods | Full objects | Component mapping and Pod labels |
| Selected Karta kinds and evaluated roots | Full objects | Workload status and labels |
| Other ancestors | Metadata, unless already watched in full | Owner references and object identity |

`Reader.GetMetadata` calls `ensureWatch(ctx, ref.GVK, ref.Namespace, true)`
to request metadata-only ancestor watches. It does not perform an API GET for
each owner. If the API rejects metadata requests, the source uses the dynamic client
and strips the response to metadata. All accepted objects omit managed fields.

When an ancestor becomes an evaluated root, `ensureWatch` requests full objects.
The metadata watch remains active until the replacement accepts a complete full
snapshot. Workers wait and retry while that snapshot is pending. A retired
watch's callbacks cannot repopulate the cache.

## Pod and workload evaluation

`reconcilePod` reads the full Pod, checks selection, and asks discovery to follow
its controller owner references. It registers the Pod under its root before
reading the root's full object. That ordering lets a root event queue the Pod
while evaluation is still running.

The root's Karta definition maps the Pod to a component, instance and replica
group when present. For Pod labels, the resolver receives this input:

```go
labelresolver.Input{
    Metrics:   []string{labelresolver.MetricPodWorkloadInfo},
    Workload:  deploymentObject, // Full Deployment web.
    PodObject: podObject,        // Full Pod web-7 as accepted from Kubernetes.
}
```

The resulting labels are saved on the Pod record. Raw Pod input preserves absent
fields and fields unknown to the typed Kubernetes client. `reconcileWorkload`
separately evaluates the full root's status and labels and publishes a workload
record. A non-Pod root without a definition can still supply ownership and
creation information. A bare Pod requires a Pod definition to be included.

Objects matching a selected Karta kind enter the workload queue even with no
Pods. `resolveSelectedObjectRoot` discovers their roots and registers membership.
For example, a selected Job owned by a Deployment registers that Deployment.
Only a root-UID job publishes a workload result. A selected-object job registers
or removes membership and queues the affected root.

Selector settings from configuration and environment variables are fixed when
the runtime is constructed. Their inputs are not fixed:

| Setting | Labels checked |
| --- | --- |
| `PodSelector` | The current Pod |
| `ObjectSelector` | The discovered root |
| `NamespaceSelector` | The Namespace object |

A watched object can enter or leave a fixed selector when its labels change.
Workers therefore check current labels before evaluation.

Pod selection filters leaf work, but unselected Pods remain cached because they
may own other Pods. A Pod leaving the selector is queued to remove its previous
result. A change in object-selector membership triggers a scope rescan. Namespace
label changes refresh admitted scopes and retire watches for excluded namespaces.

## State and publication checks

`Reader` and `controllerState` share one `objectCache` and its `stateMu`.
This mutex protects cache acceptance, ownership membership and publication.
Discovery and expression evaluation run outside it. Accepted objects are
immutable, and state methods return values or owned snapshots.

Read-only state operations use `stateMu.RLock`. Cache, membership and publication
changes use `stateMu.Lock`. Each acquisition immediately defers its release.
Helpers that require the caller's lock do not acquire it again. For example,
`publishPod` checks `isPathCurrent`,
`hasRootOwnership` and `isDefinitionCurrent`, then publishes before releasing the
lock.

Queues do not replace this mutex. A queue prevents concurrent processing of the
same key, but different keys, both evaluation queues and reflector callbacks
share state. Validation and publication must stay in one critical section so a
source event cannot change the checked objects before the result is saved.

`Reader.read` holds the read lock while `lookupObject` checks the accepted object
and source health. `Reader.mapperMu` only serializes REST mapper resets. It is
separate so API discovery cannot hold up cache reads or publication.

Before publishing, the controller checks that the input path still has the same
UIDs and resource versions, the root is still a root, and the selected definition
has not changed. Workload publication also checks that the root tracking entry
has not been removed and recreated since the snapshot. Its `generation` identifies
that entry's lifetime, not each membership update. A stale evaluation returns an
error and retries instead of replacing a newer result. Pod and workload
publications are independent.

A root change queues that root and its Pods. An intermediate owner change queues
a scope rescan so unchanged Pods can discover the new root. Definition changes
requeue affected selected objects, roots, member Pods and unresolved Pods. An
inventory read failure retains the prior selection and records its error;
it does not mean that all definitions were removed.

## Failures and cleanup

Returned worker errors use exponential retry from 10 milliseconds to 10 seconds.
Successful work clears the retry history. Pod evaluation and invalid-definition
failures are recorded without a timed retry; changed inputs trigger reevaluation.
Workload status failures are recorded on the workload result.

Failures retain a previous successful result when one exists and attach the
current error. A Pod whose owner has never resolved has `HasValue: false` and no
invented workload identity. Source health remains separate from those results.

Confirmed Pod deletion removes its result and membership. Confirmed root deletion
withdraws the workload result and requeues its members. A still-live Pod can
retain its previous mapping with an unresolved-owner error. An object recreated
under the same name has a different UID and cannot satisfy the old owner
reference. A root gaining a controller owner is also withdrawn so its members can
rediscover the new root.

Removing a namespace retires its watches and queues cached removals. A failed or
malformed LIST retains the accepted cache. A deletion event with a different UID
from the cached object is ignored. Removing a definition reevaluates its objects
against the inventory's replacement or fallback selection.

The adjacent tests cover publication freshness, ownership changes, definition
changes, source replacement and recovery, selector cleanup, and cancellation.
Run them from the exporter module with `go test -race ./pkg/controller`.
