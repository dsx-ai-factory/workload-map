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

interface DiscoveryCache {
  cluster: string;
  // The /apis promise rather than its result, so concurrent runs share it.
  groupVersions: Promise<Set<string>> | null;
  served: Map<string, ServedKind>;
  failed: Set<string>;
  pending: Map<string, Promise<void>>;
  // Group/versions already resolved, including those the cluster does not
  // serve: those contribute no kinds and asking again would gain nothing.
  asked: Set<string>;
}

function newCache(cluster: string): DiscoveryCache {
  return {
    cluster,
    groupVersions: null,
    served: new Map(),
    failed: new Set(),
    pending: new Map(),
    asked: new Set(),
  };
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

    if (cache.current.cluster !== cluster) {
      // Another cluster's answers describe different paths entirely.
      cache.current = newCache(cluster);
      setServed(null);
      setFailedGroupVersions(new Set());
    }
    setError(null);

    if (cluster === '' || requested === '') {
      // Nothing to ask: settle rather than wait on a request never made.
      setLoading(false);
      return;
    }

    const wanted = requested.split(',');
    const outstanding = wanted.filter(groupVersion => !cache.current.asked.has(groupVersion));
    if (outstanding.length === 0) {
      setServed(new Map(cache.current.served));
      setFailedGroupVersions(new Set(cache.current.failed));
      setLoading(false);
      return;
    }
    setLoading(true);

    (async () => {
      // A rejected promise would otherwise be cached as an answer, so a single
      // hiccup would keep every later wave from ever asking again.
      cache.current.groupVersions ??= fetchGroupVersions(cluster).catch((err: unknown) => {
        cache.current.groupVersions = null;
        throw err;
      });
      const groupVersions = await cache.current.groupVersions;

      await Promise.all(
        outstanding.map(groupVersion => {
          const inFlight = cache.current.pending.get(groupVersion);
          if (inFlight) {
            return inFlight;
          }
          // A group the cluster does not serve is answered too: it contributes
          // no kinds, and asking would be the 404 this exists to avoid.
          if (!groupVersions.has(groupVersion)) {
            cache.current.asked.add(groupVersion);
            return Promise.resolve();
          }
          const resolving = fetchResources(cluster, groupVersion).then(resolved => {
            cache.current.pending.delete(groupVersion);
            if (resolved.failed) {
              // Retried on the next wave, rather than silence passing for an
              // answer.
              cache.current.failed.add(groupVersion);
              return;
            }
            cache.current.failed.delete(groupVersion);
            cache.current.asked.add(groupVersion);
            for (const [key, value] of resolved.served) {
              cache.current.served.set(key, value);
            }
          });
          cache.current.pending.set(groupVersion, resolving);
          return resolving;
        })
      );
    })()
      .then(() => {
        if (!cancelled) {
          setServed(new Map(cache.current.served));
          setFailedGroupVersions(new Set(cache.current.failed));
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

    return () => {
      cancelled = true;
    };
  }, [cluster, requested]);

  return { served, failedGroupVersions, loading, error };
}
