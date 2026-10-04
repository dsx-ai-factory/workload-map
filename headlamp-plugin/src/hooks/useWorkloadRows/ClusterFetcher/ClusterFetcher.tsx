// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { useCallback, useEffect, useMemo, useRef } from 'react';
import { useKartaDefinitions } from '../../useKartaDefinitions/useKartaDefinitions';
import { servedKindKey, useServedKinds } from '../../useServedKinds/useServedKinds';
import { KindFetcher } from '../KindFetcher/KindFetcher';
import { WorkloadRow } from '../workloadRow.types';

export interface ClusterState {
  // Definitions or discovery still resolving for this cluster.
  loading: boolean;
  // This cluster produced nothing at all. Other clusters may still have.
  error: Error | null;
  // Definition names that will report rows, so a caller knows what to wait for.
  expectedKinds: string[];
  // Definition name to the reason its group could not be asked about.
  discoveryFailures: Record<string, Error>;
}

export interface ClusterFetcherProps {
  cluster: string;
  onRows: (cluster: string, key: string, rows: WorkloadRow[]) => void;
  onError: (cluster: string, key: string, error: Error) => void;
  onState: (cluster: string, state: ClusterState) => void;
}

// One per selected cluster. Definitions and discovery are answered per cluster
// and each needs its own hook call, which the Rules of Hooks forbid in a loop,
// so the per-cluster work lives in a component. Renders nothing itself.
export function ClusterFetcher({ cluster, onRows, onError, onState }: ClusterFetcherProps) {
  const {
    definitions,
    loading: definitionsLoading,
    error: definitionsError,
  } = useKartaDefinitions(cluster);
  const {
    served,
    failedGroupVersions,
    loading: discoveryLoading,
    error: discoveryError,
  } = useServedKinds(
    cluster,
    definitions.map(definition => definition.karta.spec?.structureDefinition?.rootComponent?.kind)
  );

  const fetchable = useMemo(() => {
    if (served === null) {
      return [];
    }
    return definitions.flatMap(definition => {
      const kind = definition.karta.spec?.structureDefinition?.rootComponent?.kind;
      const servedKind = kind && served.get(servedKindKey(kind.group, kind.version, kind.kind));
      return servedKind ? [{ definition, ...servedKind }] : [];
    });
  }, [definitions, served]);

  const discoveryFailures = useMemo(() => {
    const failures: Record<string, Error> = {};
    for (const definition of definitions) {
      const kind = definition.karta.spec?.structureDefinition?.rootComponent?.kind;
      if (!kind) {
        continue;
      }
      const groupVersion = kind.group ? `${kind.group}/${kind.version}` : kind.version;
      if (failedGroupVersions.has(groupVersion)) {
        failures[definition.karta.metadata.name] = new Error(
          `discovery failed for ${groupVersion}`
        );
      }
    }
    return failures;
  }, [definitions, failedGroupVersions]);

  // useKartaDefinitions rebuilds its objects every render, so the derived
  // values do too. The effect keys off their content, and reads the objects
  // through refs, or reporting state would loop.
  const expectedKinds = fetchable.map(({ definition }) => definition.karta.metadata.name);
  const expectedKey = [...expectedKinds].sort().join(',');
  const failuresKey = Object.keys(discoveryFailures).sort().join(',');
  const latest = useRef({ expectedKinds, discoveryFailures });
  latest.current = { expectedKinds, discoveryFailures };

  const loading = definitionsLoading || discoveryLoading;
  const error = definitionsError ?? discoveryError ?? null;

  useEffect(() => {
    onState(cluster, {
      loading,
      error,
      expectedKinds: latest.current.expectedKinds,
      discoveryFailures: latest.current.discoveryFailures,
    });
  }, [cluster, loading, error, expectedKey, failuresKey, onState]);

  // Tagging with the cluster here keeps KindFetcher unaware of it, and the
  // callbacks stable so its effect does not re-run every render.
  const handleRows = useCallback(
    (key: string, rows: WorkloadRow[]) => onRows(cluster, key, rows),
    [cluster, onRows]
  );
  const handleError = useCallback(
    (key: string, kindError: Error) => onError(cluster, key, kindError),
    [cluster, onError]
  );

  return (
    <>
      {fetchable.map(({ definition, plural, namespaced }) => (
        <KindFetcher
          key={definition.karta.metadata.name}
          definition={definition}
          cluster={cluster}
          plural={plural}
          namespaced={namespaced}
          onRows={handleRows}
          onError={handleError}
        />
      ))}
    </>
  );
}
