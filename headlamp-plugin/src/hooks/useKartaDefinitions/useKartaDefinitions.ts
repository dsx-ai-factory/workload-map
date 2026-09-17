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

export function useKartaDefinitions(): UseKartaDefinitionsResult {
  const [catalog, setCatalog] = useState<Karta[]>([]);
  const [catalogError, setCatalogError] = useState<Error | null>(null);
  const [catalogLoading, setCatalogLoading] = useState(true);
  const [clusterKartas, clusterError] = KartaCR.useList();

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
  const installed = !clusterLoading && clusterError === null;
  const cluster = installed ? (clusterKartas ?? []).map(item => item.jsonData as Karta) : [];

  return {
    definitions: mergeDefinitions(catalog, cluster),
    installed,
    crdMissing,
    loading: catalogLoading || clusterLoading,
    error: catalogError ?? (clusterError && !crdMissing ? clusterError : null),
  };
}
