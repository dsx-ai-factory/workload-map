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
  error: Error | null;
  // Per-definition listing failures, keyed by Karta definition name (one
  // KindFetcher/error per definition) — lets a consumer distinguish "this
  // kind failed to load" from "this kind has zero instances" instead of
  // collapsing every failure into the single `error` above.
  errorsByKind: Record<string, Error>;
  // Renders nothing, but must be mounted alongside whatever consumes rows:
  // these are what subscribe to each kind's live list.
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
  const { served, loading: discoveryLoading } = useServedKinds(
    cluster ?? '',
    allDefinitions.map(
      definition => definition.karta.spec?.structureDefinition?.rootComponent?.kind
    )
  );

  // Only served kinds get a fetcher: the catalog describes far more than any
  // one cluster installs, and discovery supplies the plural and scope.
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

  const onRows = useCallback((key: string, rows: WorkloadRow[]) => {
    setRowsByKey(prev => ({ ...prev, [key]: rows }));
    setErrorsByKey(prev => {
      if (!(key in prev)) {
        return prev;
      }
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }, []);

  const onError = useCallback((key: string, error: Error) => {
    setErrorsByKey(prev => ({ ...prev, [key]: error }));
  }, []);

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

  // A fetcher counts as reported once it calls onRows or onError. Without
  // this, loading would clear before any list resolved, showing an empty set.
  const stillFetchingKinds = fetchable.some(
    ({ definition }) =>
      !(definition.karta.metadata.name in rowsByKey) &&
      !(definition.karta.metadata.name in errorsByKey)
  );
  const loading = engineLoading || definitionsLoading || discoveryLoading || stillFetchingKinds;
  const error = engineError ?? definitionsError ?? Object.values(errorsByKey)[0] ?? null;
  const rows = loading
    ? null
    : fetchable.flatMap(({ definition }) => rowsByKey[definition.karta.metadata.name] ?? []);

  return { rows, loading, error, errorsByKind: errorsByKey, fetchers };
}
