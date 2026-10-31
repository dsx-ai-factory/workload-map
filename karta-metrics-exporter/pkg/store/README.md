<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Publication Store

Store holds completed Pod mapping and workload results in separate UID maps.
`New()` returns `PublicationStore`, which combines `Writer` and `Capturer`.
Runtime receives `Writer`; collection receives `Capturer`.

Each method uses one private Store lock. Upsert clones the input struct and all
mutable maps and slices before replacing the UID entry. Callers must keep inputs
stable during the call and may reuse them afterward. Lookup and capture return
shared result pointers that callers must never mutate. Go does not enforce this
read-only convention.

`Capture` copies result pointers, source health and discovery failures under one
read lock. Its containers belong to the capture. Later replacements, deletion
and health changes cannot change an existing view. Iteration order is unspecified.
Consumers process the view after the lock is released. Separate Pod and workload
writes may appear in different revisions in a single capture.

`Valid(sources, reason)` uses only the captured source health. A saved operation
error wins. Otherwise any permission denial wins, followed by missing, unsynced
or failed sources as `api_unavailable`. Healthy or empty dependencies return
`none`. Keys match the exact resource group, version, resource and collection
namespace. An all-namespace collection uses the empty namespace.

`HasValue` is independent of validity. Runtime constructs replacements containing
retained successful values and current errors. Store does not merge or evaluate
results. Workload `Error` applies to status and creation; `StatusError` applies
only to status. `CreationSources` identifies creation's separate dependencies.
Discovery failures are stored separately by input UID.

There is no clock or TTL. Accepted source recovery can restore source-only
validity before reconciliation completes. A saved evaluation error persists
until Runtime publishes its replacement. Source recovery does not prove that a
retained calculation used the recovered input.

Removal affects only the named UID in its own domain. A workload removal leaves
Pod mappings intact. The same UID may occupy both domains. Reassignment replaces
a Pod's entire mapping and root identity once. Runtime owns event ordering,
source recovery, dependent cleanup and protection against stale work publishing
after deletion. An earlier capture may finish emitting removed results.
