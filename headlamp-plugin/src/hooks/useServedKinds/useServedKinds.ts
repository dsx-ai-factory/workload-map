// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { ApiProxy } from '@kinvolk/headlamp-plugin/lib';
import { useEffect, useState } from 'react';
import { GroupVersionKind } from '../../lib/karta/karta.types';

export interface ServedKind {
  // The resource's real plural name, which only discovery knows: MPIJob is
  // "mpijobs" but Milvus is "milvuses", and no rule derives both.
  plural: string;
  namespaced: boolean;
}

export interface UseServedKindsResult {
  // null until discovery settles, and when it failed, so a caller can tell
  // "nothing is served" from "we do not know what is served".
  served: Map<string, ServedKind> | null;
  loading: boolean;
  error: Error | null;
}

// servedKindKey identifies one kind. Group is empty for core kinds such as
// Pod, matching the groupVersion strings discovery reports.
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

async function fetchServedKinds(groupVersions: string[]): Promise<Map<string, ServedKind>> {
  // Ask which groups exist before asking what is in them, because requesting
  // the resource list of a group the cluster does not serve is itself a 404.
  const groupList = (await ApiProxy.request('/apis', {}, false, true)) as ApiGroupList;
  const servedGroupVersions = new Set<string>(['v1']);
  for (const group of groupList.groups ?? []) {
    for (const version of group.versions ?? []) {
      if (version.groupVersion) {
        servedGroupVersions.add(version.groupVersion);
      }
    }
  }

  const wanted = groupVersions.filter(groupVersion => servedGroupVersions.has(groupVersion));
  const resourceLists = await Promise.all(
    wanted.map(async groupVersion => {
      const list = (await ApiProxy.request(
        discoveryPath(groupVersion),
        {},
        false,
        true
      )) as ApiResourceList;
      return [groupVersion, list] as const;
    })
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
  return served;
}

// useServedKinds reports which of the given kinds the cluster actually serves.
// Listing a kind it does not serve returns a 404 that the query layer retries
// with backoff, and one such kind delays the whole table by several seconds,
// so the check has to be per kind: a group can be served while the kind is
// not, as with MPIJob under kubeflow.org/v1.
export function useServedKinds(kinds: (GroupVersionKind | undefined)[]): UseServedKindsResult {
  const [served, setServed] = useState<Map<string, ServedKind> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  // The caller rebuilds its kinds array every render, so the effect keys off
  // the group/versions it needs rather than the array's identity.
  const requested = [
    ...new Set(kinds.filter(kind => !!kind).map(kind => (kind.group ? `${kind.group}/${kind.version}` : kind.version))),
  ]
    .sort()
    .join(',');

  useEffect(() => {
    if (requested === '') {
      return;
    }
    let cancelled = false;

    fetchServedKinds(requested.split(','))
      .then(result => {
        if (!cancelled) {
          setServed(result);
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
  }, [requested]);

  return { served, loading, error };
}
