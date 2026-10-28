<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Runtime

`Runner.Run(ctx)` owns the watches and workers. It returns after they stop.
`Dependencies` gives Runtime discovery, the definition inventory, label resolution
and the Store writer. Collector receives only Store's read interface.

Runtime will always watch Pods in selected namespaces. It will share root watches
by resource and namespace, including roots returned by discovery without an
existing watch. It will not add intermediate-owner watches.

After resolving a root, Runtime calculates status, Pod mappings and configured
labels. It serializes final input verification and publication with accepted
events. Store locks its own data internally. Deleting a root removes its publication. A failed read changes validity
instead of pretending the workload disappeared.

This package has no queue, watch or reconcile implementation yet. The runnable
bootstrap package supplies a mock `Runner` that waits for cancellation.
