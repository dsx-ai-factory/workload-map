<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Publication Store

Runtime publishes one immutable result per root UID. Collector calls `Capture`
to read publications and resource health from one point in time.

`Writer` exposes Runtime's operations. `Capturer` exposes only the read operation.
`PublicationStore` combines them for startup wiring.

## Synchronization

Store owns its mutex. Every method is safe to call concurrently and completes
atomically. `Capture` fixes one time, record references, health and failure state
under the Store lock. It releases the lock before Collector walks the view.
Pass a fake clock through `Options.Now` in tests. Nil uses `time.Now`.

Runtime owns input verification and operation ordering. It serializes accepted
events with final verification and publication. A `Published` read followed by
`Publish` is two operations, so Runtime must serialize that sequence too. It
rechecks deletion and input versions before publishing. Store's mutex does not
make a stale calculation current.

Collection can happen between writes. Runtime must invalidate affected evidence
before clearing a shared failure. Only newly verified data can become valid.
Failure updates use the same Runtime ordering as publications. One Runtime owner
replaces the complete health list.

## Operations

- `Publish` replaces a root's complete publication. Do not mutate it afterward.
- `Published` returns the immutable publication, or nil.
- `RemovePublication` removes the root and its Pod rows. An absent UID is safe.
- `SetPublicationFailure` sets a shared failure for a key in
  `Evidence.Dependencies`. An empty reason clears it. The key identifies a shared
  dependency, such as a resource watch, rather than a workload UID.
- `SetPublicationHealth` replaces the resource health list.
- `Capture` returns a view that later writes cannot change.

The data types and interfaces are included here. Storage, internal locking,
expiry checks and copy-on-write Pod updates are not implemented. The implementation
will add `Publication.WithPod(uid, replacement)` and `CapturedView.Valid(evidence)`,
including the captured view's private failure state. `Valid` checks explicit
failure, dependency failures and expiry at the captured time.

Store owns its data types and does not import discovery. Runtime translates the
resolved identity into a publication.
