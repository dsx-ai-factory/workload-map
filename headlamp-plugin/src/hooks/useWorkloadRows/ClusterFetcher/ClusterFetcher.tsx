// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { useCallback, useEffect, useMemo, useRef } from 'react';
import { useKartaDefinitions } from '../../useKartaDefinitions/useKartaDefinitions';
import { servedKindKey, useServedKinds } from '../../useServedKinds/useServedKinds';
import { KindFetcher } from '../KindFetcher/KindFetcher';
import { WorkloadRow } from '../workloadRow.types';

export interface ClusterState {
  // Definitions or discovery still resolving for this cluster.
  loading: boolean;
  // This cluster produced nothing usable at all.
  error: Error | null;
  // Something failed but the cluster is still usable, so this belongs beside
  // its rows rather than in place of them.
  warning: Error | null;
  // Definition names that will report rows, so a caller knows what to wait for.
  expectedKinds: string[];
  // Definition name to the reason its group could not be asked about.
  discoveryFailures: Record<string, Error>;
}

export interface ClusterFetcherProps {
  cluster: string;
  namespaces?: string[];
  // Bumped to retry the catalog, which is read through the engine.
  attempt?: number;
  onRows: (cluster: string, key: string, rows: WorkloadRow[]) => void;
  onError: (cluster: string, key: string, error: Error) => void;
  onState: (cluster: string, state: ClusterState) => void;
}

// One per selected cluster. Definitions and discovery are answered per cluster
// and each needs its own hook call, which the Rules of Hooks forbid in a loop,
// so the per-cluster work lives in a component. Renders nothing itself.
export function ClusterFetcher({
  cluster,
  namespaces,
  attempt,
  onRows,
  onError,
  onState,
}: ClusterFetcherProps) {
  const {
    definitions,
    loading: definitionsLoading,
    error: definitionsError,
  } = useKartaDefinitions(cluster, attempt);
  const {
    served,
    failedGroupVersions,
    loading: discoveryLoading,
    error: discoveryError,
  } = useServedKinds(
    cluster,
    definitions.map(definition => definition.karta.spec?.structureDefinition?.rootComponent?.kind)
  );

  // An empty intersection must not reach useList: it builds one request per
  // namespace, and no namespaces means a request with none, which lists the
  // whole cluster. Selecting a namespace this cluster disallows would widen
  // the read instead of narrowing it.
  const allowed = K8s.cluster.getAllowedNamespaces(cluster);
  const visibleNamespaces =
    !namespaces || allowed.length === 0
      ? namespaces
      : namespaces.filter(namespace => allowed.includes(namespace));
  const nothingVisible = !!visibleNamespaces && visibleNamespaces.length === 0;

  const { fetchable, hiddenByNamespace } = useMemo(() => {
    if (served === null) {
      return { fetchable: [], hiddenByNamespace: 0 };
    }
    let hidden = 0;
    const list = definitions.flatMap(definition => {
      const kind = definition.karta.spec?.structureDefinition?.rootComponent?.kind;
      const servedKind = kind && served.get(servedKindKey(kind.group, kind.version, kind.kind));
      if (!servedKind) {
        return [];
      }
      // Only namespaced kinds are affected: useList drops the namespaces for a
      // cluster-scoped one, so it reads the same whatever is selected.
      if (servedKind.namespaced && nothingVisible) {
        hidden += 1;
        return [];
      }
      return [{ definition, ...servedKind }];
    });
    return { fetchable: list, hiddenByNamespace: hidden };
  }, [definitions, served, nothingVisible]);

  // Otherwise the table is simply empty, with nothing saying why.
  const namespaceWarning = useMemo(
    () =>
      hiddenByNamespace > 0
        ? new Error('the selected namespaces are not allowed on this cluster')
        : null,
    [hiddenByNamespace]
  );

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
  // Failing to read the cluster's Karta CRs is not fatal while the catalog
  // still describes kinds: listing those needs no permission on kartas.run.ai,
  // so a user without it still gets a full table.
  const usable = definitions.length > 0;
  const error = (usable ? null : definitionsError) ?? discoveryError ?? null;
  const warning = namespaceWarning ?? (usable ? definitionsError : null);

  useEffect(() => {
    onState(cluster, {
      loading,
      error,
      warning,
      expectedKinds: latest.current.expectedKinds,
      discoveryFailures: latest.current.discoveryFailures,
    });
  }, [cluster, loading, error, warning, expectedKey, failuresKey, onState]);

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
          namespaces={visibleNamespaces}
          onRows={handleRows}
          onError={handleError}
        />
      ))}
    </>
  );
}
