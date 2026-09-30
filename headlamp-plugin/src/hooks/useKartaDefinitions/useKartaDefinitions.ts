// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { useEffect, useState } from 'react';
import { Definition, KartaCR, mergeDefinitions } from '../../lib/karta/definitions';
import { Karta } from '../../lib/karta/karta.types';
import { listCatalog } from '../../lib/karta/kartaUtil';

export interface UseKartaDefinitionsResult {
  definitions: Definition[];
  installed: boolean;
  crdMissing: boolean;
  loading: boolean;
  error: Error | null;
}

// useKartaDefinitions resolves the definitions that describe workloads on one
// cluster. The cluster is required rather than optional because useList()
// defaults to every selected cluster, and definitions merge by root GVK: two
// clusters each defining Deployment would collapse into one entry, leaving a
// workload liable to be read through the other cluster's definition.
export function useKartaDefinitions(cluster: string): UseKartaDefinitionsResult {
  const [catalog, setCatalog] = useState<Karta[]>([]);
  const [catalogError, setCatalogError] = useState<Error | null>(null);
  const [catalogLoading, setCatalogLoading] = useState(true);
  const [clusterKartas, clusterError] = KartaCR.useList({ cluster });

  useEffect(() => {
    let cancelled = false;

    listCatalog()
      .then(list => {
        if (!cancelled) {
          setCatalog(list);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setCatalogError(err instanceof Error ? err : new Error(String(err)));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setCatalogLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  // useList reports a pending request as a null list with no error. Until it
  // settles the cluster is neither known to have Karta installed nor known to
  // have none, so reporting installed early would show catalog definitions as
  // the whole truth.
  const clusterLoading = clusterKartas === null && clusterError === null;
  const crdMissing = clusterError?.status === 404;

  // Only a 404 means the definitions are gone. Every other failure left them
  // in place and useList keeps serving the last successful list through one,
  // so they are kept and the error reported alongside them. The test is what
  // the list holds rather than which error came back, because a dropped
  // connection arrives as an Error carrying no status to classify.
  const clusterDefinitions = crdMissing
    ? []
    : (clusterKartas ?? []).map(item => item.jsonData as Karta);

  // Having received a list is what proves the CRD is there, so a failed
  // refresh after a success still counts as installed, while a first attempt
  // that never landed does not.
  const installed = clusterKartas !== null && !crdMissing;

  return {
    definitions: mergeDefinitions(catalog, clusterDefinitions),
    installed,
    crdMissing,
    loading: catalogLoading || clusterLoading,
    error: catalogError ?? (clusterError && !crdMissing ? clusterError : null),
  };
}
