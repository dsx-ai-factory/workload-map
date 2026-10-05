// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import { useKartaWasm } from '../useKartaWasm/useKartaWasm';
import { useSelectedNamespaces } from '../useSelectedNamespaces/useSelectedNamespaces';
import { ClusterFetcher, ClusterState } from './ClusterFetcher/ClusterFetcher';
import { WorkloadRow } from './workloadRow.types';

export interface UseWorkloadRowsResult {
  rows: WorkloadRow[] | null;
  loading: boolean;
  // Set only when nothing could be fetched on any cluster, so a consumer can
  // hide the view.
  error: Error | null;
  // The engine failed to load. Rows are metadata and survive without it, so
  // this costs the status column rather than the table.
  engineError: Error | null;
  // Partial failures, keyed "cluster/definition". The other kinds loaded, so
  // these belong beside the results rather than in place of them.
  errorsByKind: Record<string, Error>;
  // Clusters that produced nothing while others did, keyed by cluster. A
  // consumer may take these as covering every kind beneath them.
  errorsByCluster: Record<string, Error>;
  // Clusters that produced rows despite something failing. Their kinds can
  // still fail independently, so these explain nothing about the kinds.
  warningsByCluster: Record<string, Error>;
  // Renders nothing; mounting these is what subscribes to each kind's list.
  fetchers: ReactNode;
}

// Merges the definitions (cluster CRs plus embedded catalog) with a live
// per-kind instance list and projects each instance into a row, across every
// selected cluster.
export function useWorkloadRows(): UseWorkloadRowsResult {
  const { error: engineError, loading: engineLoading } = useKartaWasm();
  // useSelectedClusters returns an empty list when nothing is explicitly
  // selected, which is the ordinary single-cluster case, so fall back to the
  // current one. Same shape as knative's useClusters.
  const selectedClusters = K8s.useSelectedClusters();
  const currentCluster = K8s.useCluster();
  const selectedKey = selectedClusters.join(',');
  const namespaces = useSelectedNamespaces();
  const clusters = useMemo(
    () =>
      selectedClusters.length > 0 ? selectedClusters : currentCluster ? [currentCluster] : [],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selectedKey, currentCluster]
  );

  // Entries are scoped by namespace selection as well as cluster. Wiping them
  // on change instead would race the fetchers, whose effects run first.
  const namespaceKey = (namespaces ?? []).join(',');

  const [rowsByKey, setRowsByKey] = useState<Record<string, WorkloadRow[]>>({});
  const [errorsByKey, setErrorsByKey] = useState<Record<string, Error>>({});
  const [clusterStates, setClusterStates] = useState<Record<string, ClusterState>>({});

  // Definition names repeat across clusters and selections, so every entry is
  // scoped by both.
  const scopedKey = useCallback(
    (cluster: string, key: string) => `${cluster}/${namespaceKey}/${key}`,
    [namespaceKey]
  );

  const onRows = useCallback(
    (cluster: string, key: string, rows: WorkloadRow[]) => {
    const scoped = scopedKey(cluster, key);
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
    (cluster: string, key: string, error: Error) => {
      setErrorsByKey(prev => ({ ...prev, [scopedKey(cluster, key)]: error }));
    },
    [scopedKey]
  );

  const onState = useCallback((cluster: string, state: ClusterState) => {
    setClusterStates(prev => ({ ...prev, [cluster]: state }));
  }, []);

  // A deselected cluster's entries would otherwise satisfy the loading gate if
  // it were selected again, since discovery names the same kinds before any
  // list has resolved, and a failing list would leave them indefinitely.
  useEffect(() => {
    const keep = <T,>(entries: Record<string, T>) => {
      const kept = Object.fromEntries(
        Object.entries(entries).filter(([key]) => clusters.includes(key.slice(0, key.indexOf('/'))))
      );
      return Object.keys(kept).length === Object.keys(entries).length ? entries : kept;
    };
    setRowsByKey(keep);
    setErrorsByKey(keep);
    setClusterStates(prev =>
      Object.keys(prev).every(cluster => clusters.includes(cluster))
        ? prev
        : Object.fromEntries(Object.entries(prev).filter(([cluster]) => clusters.includes(cluster)))
    );
  }, [clusters]);

  const fetchers = useMemo(
    () =>
      clusters.map(cluster => (
        <ClusterFetcher
          key={cluster}
          cluster={cluster}
          namespaces={namespaces}
          onRows={onRows}
          onError={onError}
          onState={onState}
        />
      )),
    [clusters, namespaces, onRows, onError, onState]
  );

  // Every selected cluster has to have reported its definitions, and every
  // kind it expects has to have reported rows or an error.
  const stillFetching = clusters.some(cluster => {
    const state = clusterStates[cluster];
    if (!state || state.loading) {
      return true;
    }
    return state.expectedKinds.some(
      name => !(scopedKey(cluster, name) in rowsByKey) && !(scopedKey(cluster, name) in errorsByKey)
    );
  });

  // Kept apart: a cluster that still produced rows explains nothing about a
  // kind that failed under it, so folding the two would hide real kind errors.
  const errorsByCluster = useMemo(() => {
    const scoped: Record<string, Error> = {};
    for (const cluster of clusters) {
      const clusterError = clusterStates[cluster]?.error;
      if (clusterError) {
        scoped[cluster] = clusterError;
      }
    }
    return scoped;
  }, [clusters, clusterStates]);

  const warningsByCluster = useMemo(() => {
    const scoped: Record<string, Error> = {};
    for (const cluster of clusters) {
      const warning = clusterStates[cluster]?.warning;
      if (warning) {
        scoped[cluster] = warning;
      }
    }
    return scoped;
  }, [clusters, clusterStates]);

  const errorsByKind = useMemo(() => {
    const scoped: Record<string, Error> = {};
    for (const cluster of clusters) {
      const state = clusterStates[cluster];
      if (!state) {
        continue;
      }
      for (const name of state.expectedKinds) {
        const kindError = errorsByKey[scopedKey(cluster, name)];
        if (kindError) {
          scoped[`${cluster}/${name}`] = kindError;
        }
      }
      for (const [name, failure] of Object.entries(state.discoveryFailures)) {
        scoped[`${cluster}/${name}`] = failure;
      }
    }
    return scoped;
  }, [clusters, clusterStates, errorsByKey, scopedKey]);

  const loading = engineLoading || stillFetching;
  // Fatal only when nothing is left: one cluster failing among several is
  // partial, and reported through errorsByCluster instead.
  const everyClusterFailed =
    clusters.length > 0 && clusters.every(cluster => !!clusterStates[cluster]?.error);
  const error = everyClusterFailed
    ? clusters.map(cluster => clusterStates[cluster]?.error).find(Boolean) ?? null
    : null;

  const rows = loading
    ? null
    : clusters.flatMap(cluster =>
        (clusterStates[cluster]?.expectedKinds ?? []).flatMap(
          name => rowsByKey[scopedKey(cluster, name)] ?? []
        )
      );


  return {
    rows,
    loading,
    error,
    engineError,
    errorsByKind,
    errorsByCluster,
    warningsByCluster,
    fetchers,
  };
}
