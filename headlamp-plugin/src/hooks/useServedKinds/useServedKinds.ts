// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { ApiProxy } from '@kinvolk/headlamp-plugin/lib';
import { useEffect, useRef, useState } from 'react';
import { GroupVersionKind } from '../../lib/karta/karta.types';

export interface ServedKind {
  // Only discovery knows this: MPIJob is "mpijobs", Milvus is "milvuses".
  plural: string;
  namespaced: boolean;
}

export interface UseServedKindsResult {
  // null while loading and when discovery failed: not known, not empty.
  served: Map<string, ServedKind> | null;
  // Group/versions that did not answer, so a kind missing from `served` can be
  // told from one the cluster does not have.
  failedGroupVersions: Set<string>;
  loading: boolean;
  error: Error | null;
}

// Group is empty for core kinds, matching discovery's groupVersion strings.
export function servedKindKey(group: string, version: string, kind: string): string {
  return `${group ? `${group}/${version}` : version}/${kind}`;
}

interface ApiGroupList {
  groups?: { versions?: { groupVersion?: string }[] }[];
}

interface ApiResourceList {
  resources?: { name?: string; kind?: string; namespaced?: boolean }[];
}

function discoveryPath(groupVersion: string): string {
  // The core group is served at /api/v1; every other group lives under /apis.
  return groupVersion.includes('/') ? `/apis/${groupVersion}` : `/api/${groupVersion}`;
}

// Which group/versions the cluster serves at all. Asked before anything else,
// because requesting the resource list of a group it does not serve is itself
// a 404.
async function fetchGroupVersions(cluster: string): Promise<Set<string>> {
  const groupList = (await ApiProxy.request('/apis', { cluster }, false, true)) as ApiGroupList;
  const servedGroupVersions = new Set<string>(['v1']);
  for (const group of groupList.groups ?? []) {
    for (const version of group.versions ?? []) {
      if (version.groupVersion) {
        servedGroupVersions.add(version.groupVersion);
      }
    }
  }
  return servedGroupVersions;
}

interface ResolvedGroupVersion {
  groupVersion: string;
  served: Map<string, ServedKind>;
  failed: boolean;
}

// Reads one group/version's resources, which supply the plural and scope.
async function fetchResources(
  cluster: string,
  groupVersion: string
): Promise<ResolvedGroupVersion> {
  const served = new Map<string, ServedKind>();
  let list: ApiResourceList;
  try {
    list = (await ApiProxy.request(
      discoveryPath(groupVersion),
      { cluster },
      false,
      true
    )) as ApiResourceList;
  } catch {
    // One group failing must not discard what the others answered.
    return { groupVersion, served, failed: true };
  }

  const separator = groupVersion.lastIndexOf('/');
  const group = separator === -1 ? '' : groupVersion.slice(0, separator);
  const version = separator === -1 ? groupVersion : groupVersion.slice(separator + 1);
  for (const resource of list.resources ?? []) {
    // Subresources are reported as "pods/status" and are not listable.
    if (!resource.name || !resource.kind || resource.name.includes('/')) {
      continue;
    }
    served.set(servedKindKey(group, version, resource.kind), {
      plural: resource.name,
      namespaced: resource.namespaced !== false,
    });
  }
  return { groupVersion, served, failed: false };
}

// Short, since an API group can be installed while the page is open.
const MISSING_GROUP_TTL_MS = 30_000;

interface GroupVersionsRequest {
  sequence: number;
  // The promise rather than its result, so concurrent runs share it.
  promise: Promise<Set<string>>;
}

interface DiscoveryCache {
  cluster: string;
  groupVersions: GroupVersionsRequest | null;
  sequence: number;
  served: Map<string, ServedKind>;
  failed: Set<string>;
  pending: Map<string, Promise<void>>;
  asked: Set<string>;
  // Group/versions the cluster did not list, and when. Entries expire, since
  // the group may be installed later.
  missing: Map<string, number>;
}

function newCache(cluster: string): DiscoveryCache {
  return {
    cluster,
    groupVersions: null,
    sequence: 0,
    served: new Map(),
    failed: new Set(),
    pending: new Map(),
    asked: new Set(),
    missing: new Map(),
  };
}

function requestGroupVersions(runCache: DiscoveryCache, cluster: string): GroupVersionsRequest {
  const request: GroupVersionsRequest = {
    sequence: ++runCache.sequence,
    // Not cached on failure, or one hiccup would stop every later wave asking.
    promise: fetchGroupVersions(cluster).catch((err: unknown) => {
      if (runCache.groupVersions === request) {
        runCache.groupVersions = null;
      }
      throw err;
    }),
  };
  runCache.groupVersions = request;
  return request;
}

// Reports which of the given kinds the cluster serves, since listing one it
// does not costs a 404 retried with backoff. Per kind rather than per group:
// kubeflow.org/v1 is served, but MPIJob lives in v2beta1.
export function useServedKinds(
  cluster: string,
  kinds: (GroupVersionKind | undefined)[]
): UseServedKindsResult {
  const [served, setServed] = useState<Map<string, ServedKind> | null>(null);
  const [failedGroupVersions, setFailedGroupVersions] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  // Bumped when an absent group's answer expires, to re-run the effect while
  // the requested groups stay the same.
  const [recheck, setRecheck] = useState(0);

  // Definitions arrive in waves, the catalog first and the cluster's own CRs
  // after, so the kind set grows and this effect re-runs, often before the
  // first run has finished. Keeping what the cluster answered, and sharing the
  // in-flight promise per group/version, means a later run asks only about
  // what is new instead of repeating /apis and every group.
  const cache = useRef<DiscoveryCache>(newCache(''));

  // The caller rebuilds its kinds array every render, so the effect keys off
  // the group/versions it needs rather than the array's identity.
  const requested = [
    ...new Set(
      kinds
        .filter(kind => !!kind)
        .map(kind => (kind.group ? `${kind.group}/${kind.version}` : kind.version))
    ),
  ]
    .sort()
    .join(',');

  // Keyed by cluster too: plurals and scope are answered per cluster.
  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const cleanup = () => {
      cancelled = true;
      clearTimeout(timer);
    };

    if (cache.current.cluster !== cluster) {
      // Another cluster's answers describe different paths entirely.
      cache.current = newCache(cluster);
      setServed(null);
      setFailedGroupVersions(new Set());
    }
    setError(null);

    // Captured once, so a late answer writes into the cache its request was
    // made against. Reading cache.current in the continuation would let a
    // previous cluster's answer land in the cache of whatever is current by
    // the time it resolves.
    const runCache = cache.current;
    const runSequence = ++runCache.sequence;

    if (cluster === '' || requested === '') {
      // Nothing to ask: settle rather than wait on a request never made.
      setLoading(false);
      return;
    }

    const wanted = requested.split(',');
    const scheduleRecheck = () => {
      const expiries = wanted
        .map(groupVersion => runCache.missing.get(groupVersion))
        .filter(missingSince => missingSince !== undefined)
        .map(missingSince => missingSince + MISSING_GROUP_TTL_MS - Date.now());
      if (expiries.length > 0) {
        timer = setTimeout(() => setRecheck(n => n + 1), Math.max(0, Math.min(...expiries)));
      }
    };
    const now = Date.now();
    const outstanding = wanted.filter(groupVersion => {
      const missingSince = runCache.missing.get(groupVersion);
      return (
        !runCache.asked.has(groupVersion) &&
        (missingSince === undefined || now - missingSince >= MISSING_GROUP_TTL_MS)
      );
    });
    if (outstanding.length === 0) {
      setServed(new Map(runCache.served));
      setFailedGroupVersions(new Set(runCache.failed));
      setLoading(false);
      scheduleRecheck();
      return cleanup;
    }
    setLoading(true);

    (async () => {
      let request = runCache.groupVersions ?? requestGroupVersions(runCache, cluster);
      let groupVersions = await request.promise;
      let refreshFailed = false;
      // An answer older than this run may predate an install, so refresh it
      // before treating the group as absent. Concurrent runs share the refresh.
      if (request.sequence < runSequence && outstanding.some(gv => !groupVersions.has(gv))) {
        request =
          runCache.groupVersions && runCache.groupVersions.sequence > runSequence
            ? runCache.groupVersions
            : requestGroupVersions(runCache, cluster);
        try {
          groupVersions = await request.promise;
        } catch {
          // The earlier answer is still usable, so only the groups it cannot
          // vouch for are reported, not the whole discovery.
          refreshFailed = true;
        }
      }

      await Promise.all(
        outstanding.map(groupVersion => {
          const inFlight = runCache.pending.get(groupVersion);
          if (inFlight) {
            return inFlight;
          }
          // A group the cluster does not serve is answered too: it contributes
          // no kinds, and asking would be the 404 this exists to avoid.
          if (!groupVersions.has(groupVersion)) {
            if (refreshFailed) {
              runCache.failed.add(groupVersion);
            } else {
              runCache.missing.set(groupVersion, Date.now());
            }
            return Promise.resolve();
          }
          const resolving = fetchResources(cluster, groupVersion).then(resolved => {
            runCache.pending.delete(groupVersion);
            if (resolved.failed) {
              // Retried on the next wave, rather than silence passing for an
              // answer.
              runCache.failed.add(groupVersion);
              return;
            }
            runCache.failed.delete(groupVersion);
            runCache.missing.delete(groupVersion);
            runCache.asked.add(groupVersion);
            for (const [key, value] of resolved.served) {
              runCache.served.set(key, value);
            }
          });
          runCache.pending.set(groupVersion, resolving);
          return resolving;
        })
      );
    })()
      .then(() => {
        if (!cancelled) {
          setServed(new Map(runCache.served));
          setFailedGroupVersions(new Set(runCache.failed));
          scheduleRecheck();
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return cleanup;
  }, [cluster, requested, recheck]);

  return { served, failedGroupVersions, loading, error };
}
