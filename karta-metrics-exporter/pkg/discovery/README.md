<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Discovery

Discovery will resolve a Pod or object to its workload root and keep the shared
ownership index. A generic workload will be available without a Karta definition.
The inventory can then supply a definition for Karta to interpret status and Pod
grouping.

Ownership follows the controller-owner chain to its actual root. A missing Karta
on that root does not make an intermediate owner the root. Only an explicitly
configured ignored owner can stop traversal early.

This package will use cached inputs. The controller will own watches and retries.
Implementation follows in a separate change.
