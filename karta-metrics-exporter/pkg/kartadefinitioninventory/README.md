<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta definition inventory

`Reader` defines the definition lookup contract. Implementation follows.
`Lookup` selects for the observed root's group and kind.
The returned `GVK` identifies the selected definition's API representation.
A nil `Definition` with no error means confirmed absence after sources initialize.
An error means lookup is unresolved or unavailable. Callers ignore that result.
Source failure must not appear as absence or trigger a fallback selection.

`Definition` uses the existing SDK Karta API type.
`Origin` records the catalog or cluster source and its identity.
For cluster definitions, `UID` and `ResourceVersion` come from the Karta object.
Catalog definitions use source and name; `UID` and `ResourceVersion` are empty.
The catalog is bundled with the SDK. Updating it requires a new SDK version.
Returned selections and definitions are caller-owned snapshots.

`Config` is constructor input. Catalog and cluster sources each have an enable
flag and definition-name include/exclude lists. Cluster definitions also have a
label selector. Empty includes mean all. Names and labels must both match;
exclusions win. Selected cluster definitions take precedence over catalog ones.

The implementation will load the catalog and watch cluster Karta changes. Lookup
reads only those caches. Source failures return an error rather than falling back.
Filtering, cache updates and concurrent access remain implementation work.
