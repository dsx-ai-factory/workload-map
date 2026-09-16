// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { ReactNode, useCallback, useMemo, useState } from 'react';
import { useKartaDefinitions } from '../useKartaDefinitions';
import { useKartaWasm } from '../useKartaWasm';
import { servedKindKey, useServedKinds } from '../useServedKinds';
import { KindFetcher } from './KindFetcher';
import { WorkloadRow } from './workloadRow.types';

export interface UseWorkloadRowsResult {
  rows: WorkloadRow[] | null;
  loading: boolean;
  error: Error | null;
  // Per-definition listing failures, keyed by Karta definition name (one
  // KindFetcher/error per definition) — lets a consumer distinguish "this
  // kind failed to load" from "this kind has zero instances" instead of
  // collapsing every failure into the single `error` above (RUN-41611).
  errorsByKind: Record<string, Error>;
  // Must be rendered somewhere in the tree alongside whatever consumes rows
  // (it renders nothing visible) — this is what actually subscribes to each
  // kind's live list via useList(). See KindFetcher's own comment for why
  // this can't just live inside the hook itself.
  fetchers: ReactNode;
}

// useWorkloadRows merges useKartaDefinitions() (cluster CRs + embedded
// catalog) with a live per-kind instance list, then projects each instance
// into a WorkloadsTable row via the WASM engine (see buildRow.ts).
export function useWorkloadRows(): UseWorkloadRowsResult {
  const { error: engineError, loading: engineLoading } = useKartaWasm();
  const {
    definitions: allDefinitions,
    loading: definitionsLoading,
    error: definitionsError,
  } = useKartaDefinitions();
  const { served, loading: discoveryLoading } = useServedKinds(
    allDefinitions.map(definition => definition.karta.spec.structureDefinition.rootComponent.kind)
  );

  // Only kinds the cluster actually serves get a fetcher. The catalog
  // describes every kind Karta knows, most of which are not installed on any
  // one cluster, and listing one of those returns a 404 that the query layer
  // retries with backoff, holding up the whole table. Discovery also supplies
  // the plural and scope, which cannot be derived from the kind name.
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
          plural={plural}
          namespaced={namespaced}
          onRows={onRows}
          onError={onError}
        />
      )),
    [fetchable, onRows, onError]
  );

  // A definition's KindFetcher hasn't reported yet until it calls onRows or
  // onError at least once — without this, `loading` would flip to false as
  // soon as the engine/definitions are ready, showing an empty table for
  // however long each kind's own useList() takes to resolve its first page.
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
