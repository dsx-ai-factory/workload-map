<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta definition inventory

`Reader` defines the definition lookup contract, and `Inventory` implements it.
`Lookup` selects for the observed root's group and kind.
The returned `GVK` identifies the selected definition's API representation.
A nil `Definition` with no error means confirmed absence after sources initialize.
An error means lookup is unresolved or unavailable. Callers ignore that result.
Source failure must not appear as absence or trigger a fallback selection.

A cluster Karta for `apps/Deployment` overrides the bundled Deployment
selection. Deleting it selects the next cluster candidate or the catalog.

`Config` is constructor input. Catalog and cluster sources each have an enable
flag and definition-name include/exclude lists. Empty includes mean all;
exclusions win. Selected cluster definitions take precedence over catalog ones.

The inventory owns two maps: cluster definitions by Karta name and catalog
winners by workload group and kind. The catalog map is built once. Watch events
replace or delete cluster entries. A complete list replaces the cluster map
atomically. One `sync.RWMutex` protects cluster data and source health.

`Lookup(gvk)` checks health and scans cluster definitions under a read lock.
It ignores the requested version. Include and exclude filters apply before
election. The oldest matching cluster definition wins; name breaks ties.
When none matches, the catalog winner applies. Catalog selection prefers
Kubernetes version priority, then name. Missing root version or kind prevents
selection. Other validation errors are returned only if that definition wins.

Every cluster candidate stays in the map so deletion and root-kind changes can
select another winner. Validation runs once per decoded entry,
outside the lock, including entries that do not win. The stored result avoids
validation work during lookup. Entries are replaced whole. The winning definition
is copied after releasing the lock, so callers own their returned data. `Origin`
identifies the source and name, plus UID and resource version for cluster results.

Start `Run(ctx)` in a goroutine, then call `WaitLoaded(ctx)` before consumers.
The first complete list closes the load channel, even if empty. Multiple callers
can wait. Waiter cancellation or stopping before the first load returns an error.
A disabled cluster source is already loaded. `Run` may be started only once.

Initial loading and current health are separate. Lookup returns `ErrNotReady`
before loading. A list failure, watch error or unexpected stream closure makes
lookups unavailable, including catalog lookups. Permission errors remain distinct.
Only a fresh complete list can restore health; a failure after that list began
cannot be cleared by it. Servers without streaming-list support use ordinary LIST.
Events from a stopped watch cannot change health. Decode errors remain until the
Karta is corrected or deleted. Stopping Run makes cluster lookups unavailable;
a catalog-only inventory keeps serving.

A nil `Definition` with no error means both enabled sources are healthy and
neither has a matching definition. An invalid winner returns an error. Neither
source failure nor validation failure selects another definition.
