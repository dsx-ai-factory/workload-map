// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { ReactNode, useCallback, useMemo, useState } from 'react';
import { useKartaDefinitions } from '../useKartaDefinitions/useKartaDefinitions';
import { useKartaWasm } from '../useKartaWasm/useKartaWasm';
import { servedKindKey, useServedKinds } from '../useServedKinds/useServedKinds';
import { KindFetcher } from './KindFetcher/KindFetcher';
import { WorkloadRow } from './workloadRow.types';

export interface UseWorkloadRowsResult {
  rows: WorkloadRow[] | null;
  loading: boolean;
  // Set only when nothing could be fetched, so a consumer can hide the view.
  error: Error | null;
  // Partial failures by definition name. The other kinds loaded, so these
  // belong beside the results rather than in place of them.
  errorsByKind: Record<string, Error>;
  // Renders nothing; mounting these is what subscribes to each kind's list.
  fetchers: ReactNode;
}

// Merges the definitions (cluster CRs plus embedded catalog) with a live
// per-kind instance list and projects each instance into a row.
export function useWorkloadRows(): UseWorkloadRowsResult {
  const { error: engineError, loading: engineLoading } = useKartaWasm();
  // One cluster at a time: reading several would need one useKartaDefinitions
  // call each, which the Rules of Hooks forbid in a loop.
  const cluster = K8s.useCluster();
  const {
    definitions: allDefinitions,
    loading: definitionsLoading,
    error: definitionsError,
  } = useKartaDefinitions(cluster ?? '');
  const {
    served,
    failedGroupVersions,
    loading: discoveryLoading,
    error: discoveryError,
  } = useServedKinds(
    cluster ?? '',
    allDefinitions.map(
      definition => definition.karta.spec?.structureDefinition?.rootComponent?.kind
    )
  );

  // Only served kinds get a fetcher; discovery supplies the plural and scope.
  const fetchable = useMemo(() => {
    if (served === null) {
      return [];
    }
    return allDefinitions.flatMap(definition => {
      const kind = definition.karta.spec.structureDefinition.rootComponent.kind;
      const servedKind = kind && served.get(servedKindKey(kind.group, kind.version, kind.kind));
      return servedKind ? [{ definition, ...servedKind }] : [];
    });
  }, [allDefinitions, served]);

  const [rowsByKey, setRowsByKey] = useState<Record<string, WorkloadRow[]>>({});
  const [errorsByKey, setErrorsByKey] = useState<Record<string, Error>>({});

  // Definition names repeat across clusters. Clearing on change instead would
  // race the fetchers, whose effects run before this component's.
  const scopedKey = useCallback(
    (name: string) => `${cluster ?? ''}/${name}`,
    [cluster]
  );

  const onRows = useCallback(
    (key: string, rows: WorkloadRow[]) => {
      const scoped = scopedKey(key);
      setRowsByKey(prev => ({ ...prev, [scoped]: rows }));
      setErrorsByKey(prev => {
        if (!(scoped in prev)) {
          return prev;
        }
        const next = { ...prev };
        delete next[scoped];
        return next;
      });
    },
    [scopedKey]
  );

  const onError = useCallback(
    (key: string, error: Error) => {
      setErrorsByKey(prev => ({ ...prev, [scopedKey(key)]: error }));
    },
    [scopedKey]
  );

  const fetchers = useMemo(
    () =>
      fetchable.map(({ definition, plural, namespaced }) => (
        <KindFetcher
          key={definition.karta.metadata.name}
          definition={definition}
          cluster={cluster ?? ''}
          plural={plural}
          namespaced={namespaced}
          onRows={onRows}
          onError={onError}
        />
      )),
    [fetchable, cluster, onRows, onError]
  );

  // Without this, loading would clear before any list resolved.
  const stillFetchingKinds = fetchable.some(
    ({ definition }) =>
      !(scopedKey(definition.karta.metadata.name) in rowsByKey) &&
      !(scopedKey(definition.karta.metadata.name) in errorsByKey)
  );
  // Only errors a current fetcher owns.
  const errorsByKind = useMemo(() => {
    const scoped: Record<string, Error> = {};
    for (const { definition } of fetchable) {
      const name = definition.karta.metadata.name;
      const kindError = errorsByKey[scopedKey(name)];
      if (kindError) {
        scoped[name] = kindError;
      }
    }
    // A group that never answered would otherwise read as the kind being
    // absent.
    for (const definition of allDefinitions) {
      const kind = definition.karta.spec?.structureDefinition?.rootComponent?.kind;
      if (!kind) {
        continue;
      }
      const groupVersion = kind.group ? `${kind.group}/${kind.version}` : kind.version;
      if (failedGroupVersions.has(groupVersion)) {
        scoped[definition.karta.metadata.name] = new Error(
          `discovery failed for ${groupVersion}`
        );
      }
    }
    return scoped;
  }, [fetchable, errorsByKey, scopedKey, allDefinitions, failedGroupVersions]);

  const loading = engineLoading || definitionsLoading || discoveryLoading || stillFetchingKinds;
  // Fatal only: nothing to show at all. A single kind failing goes to
  // errorsByKind, since the kinds that loaded are still worth showing.
  const error = engineError ?? definitionsError ?? discoveryError ?? null;
  const rows = loading
    ? null
    : fetchable.flatMap(({ definition }) => rowsByKey[scopedKey(definition.karta.metadata.name)] ?? []);

  return { rows, loading, error, errorsByKind, fetchers };
}
