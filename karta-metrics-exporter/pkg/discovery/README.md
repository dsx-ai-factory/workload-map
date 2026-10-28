<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Discovery

`Get(ctx, ref)` resolves one object to its workload root. For a Pod owned by a Job
owned by a JobSet, it returns the JobSet reference and the path from Pod to root.
A missing Karta does not make the Job the root. Only an explicitly ignored owner
allows traversal to stop early.

`Dependencies` supplies the object reader and definition inventory. The reader
checks Runtime's informer cache and performs metadata GETs. Discovery does not
start watches. `MissingCoverage` requests a root watch only; intermediate owners
are not included. Runtime handles the request and retries after synchronization.

Callers inspect errors with `errors.Is`. Missing coverage can accompany a Karta
selection error once the root is known. An unresolved chain cannot request a
root watch.

The interfaces and result types are included here. Ownership indexing, input
verification, Karta evaluation handles and their batches remain implementation
work. `GenericWorkload` currently carries identity, path and resolution only.
