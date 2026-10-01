// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { ApiProxy } from '@kinvolk/headlamp-plugin/lib';
import { useEffect, useState } from 'react';
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

interface DiscoveryResult {
  served: Map<string, ServedKind>;
  failedGroupVersions: Set<string>;
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

async function fetchServedKinds(
  cluster: string,
  groupVersions: string[]
): Promise<DiscoveryResult> {
  // Ask which groups exist before asking what is in them, because requesting
  // the resource list of a group the cluster does not serve is itself a 404.
  const groupList = (await ApiProxy.request('/apis', { cluster }, false, true)) as ApiGroupList;
  const servedGroupVersions = new Set<string>(['v1']);
  for (const group of groupList.groups ?? []) {
    for (const version of group.versions ?? []) {
      if (version.groupVersion) {
        servedGroupVersions.add(version.groupVersion);
      }
    }
  }

  const wanted = groupVersions.filter(groupVersion => servedGroupVersions.has(groupVersion));
  // Settled, not all: one group failing would otherwise discard every other
  // group's answer.
  const results = await Promise.allSettled(
    wanted.map(async groupVersion => {
      const list = (await ApiProxy.request(
        discoveryPath(groupVersion),
        { cluster },
        false,
        true
      )) as ApiResourceList;
      return [groupVersion, list] as const;
    })
  );
  const resourceLists = results.flatMap(result =>
    result.status === 'fulfilled' ? [result.value] : []
  );
  const failedGroupVersions = new Set(
    wanted.filter((_, index) => results[index].status === 'rejected')
  );

  const served = new Map<string, ServedKind>();
  for (const [groupVersion, list] of resourceLists) {
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
  }
  return { served, failedGroupVersions };
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

  // The caller rebuilds its kinds array every render, so the effect keys off
  // the group/versions it needs rather than the array's identity.
  const requested = [
    ...new Set(kinds.filter(kind => !!kind).map(kind => (kind.group ? `${kind.group}/${kind.version}` : kind.version))),
  ]
    .sort()
    .join(',');

  // Keyed by cluster too: plurals and scope are answered per cluster.
  useEffect(() => {
    let cancelled = false;

    // Start clean, so a previous run's answers are not read as this run's.
    setServed(null);
    setFailedGroupVersions(new Set());
    setError(null);

    if (cluster === '' || requested === '') {
      // Nothing to ask: settle rather than wait on a request never made.
      setLoading(false);
      return;
    }
    setLoading(true);

    fetchServedKinds(cluster, requested.split(','))
      .then(result => {
        if (!cancelled) {
          setServed(result.served);
          setFailedGroupVersions(result.failedGroupVersions);
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
