// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { useEffect, useMemo, useRef } from 'react';
import { Definition } from '../../../lib/karta/definitions';
import { Workload } from '../../../lib/karta/karta.types';
import { evaluatePhases } from '../../../lib/karta/kartaUtil';
import { buildWorkloadRow } from '../buildRow';
import { WorkloadRow } from '../workloadRow.types';

export interface KindFetcherProps {
  definition: Definition;
  // Both come from discovery rather than the definition: the plural cannot be
  // derived from the kind name, and the scope is not in the Karta spec.
  plural: string;
  namespaced: boolean;
  onRows: (key: string, rows: WorkloadRow[]) => void;
  onError: (key: string, error: Error) => void;
}

interface PhaseCacheEntry {
  resourceVersion: string;
  phases: string[];
}

function workloadCacheKey(workload: Workload): string {
  return workload.metadata.uid ?? workload.metadata.name;
}

// One KindFetcher per Karta definition — the set of workload kinds is
// data-driven (comes from useKartaDefinitions() at runtime), so each needs
// its own useList() call to satisfy React's Rules of Hooks; they can't be
// called in a loop inside a single hook. Renders nothing; only exists for
// its useList() subscription and the effect that projects results into rows.
export function KindFetcher({ definition, plural, namespaced, onRows, onError }: KindFetcherProps) {
  const key = definition.karta.metadata.name;
  // Validated Karta definitions always have a root Kind (pkg/catalog and the
  // operator's admission both reject one without it), so this is safe.
  const kind = definition.karta.spec.structureDefinition.rootComponent.kind!;

  const ResourceClass = useMemo(
    () =>
      K8s.crd.makeCustomResourceClass({
        apiInfo: [{ group: kind.group, version: kind.version }],
        kind: kind.kind,
        pluralName: plural,
        singularName: kind.kind.toLowerCase(),
        isNamespaced: namespaced,
      }),
    [kind.group, kind.version, kind.kind, plural, namespaced]
  );

  const [items, error] = ResourceClass.useList();

  // Keyed by workload uid/name, so status (an evaluatePhases WASM call) is
  // only recomputed when a workload's resourceVersion actually changes, not
  // on every list poll — see RUN-41607's "Unreachable" investigation, where
  // recomputing per-row status on every poll tick across all catalog kinds
  // flooded the cluster.
  const phaseCache = useRef<Map<string, PhaseCacheEntry>>(new Map());
  // The resourceVersion each key's most recently *started* evaluatePhases
  // call was for. evaluatePhases calls can resolve out of order (a slow
  // call for an older resourceVersion finishing after a newer one already
  // completed); without this, that stale result would overwrite the fresh
  // cache entry and stick until the next poll, showing e.g. both
  // "Initializing" and "Running" chips at once for one workload.
  const inFlightVersion = useRef<Map<string, string>>(new Map());

  useEffect(() => {
    if (error) {
      onError(key, new Error(error.message));
      return;
    }
    if (!items) {
      return;
    }

    const buildRows = () =>
      items.map(item => {
        const workload = item.jsonData as Workload;
        const row = buildWorkloadRow(definition, workload, item.cluster);
        const cached = phaseCache.current.get(workloadCacheKey(workload));
        return cached ? { ...row, phases: cached.phases } : row;
      });

    onRows(key, buildRows());

    const stale = items.filter(item => {
      const workload = item.jsonData as Workload;
      const cached = phaseCache.current.get(workloadCacheKey(workload));
      return !cached || cached.resourceVersion !== (workload.metadata.resourceVersion ?? '');
    });
    if (stale.length === 0) {
      return;
    }

    stale.forEach(item => {
      const workload = item.jsonData as Workload;
      inFlightVersion.current.set(workloadCacheKey(workload), workload.metadata.resourceVersion ?? '');
    });

    let cancelled = false;
    Promise.all(
      stale.map(async item => {
        const workload = item.jsonData as Workload;
        const cacheKey = workloadCacheKey(workload);
        const resourceVersion = workload.metadata.resourceVersion ?? '';
        try {
          const phases = await evaluatePhases(definition.karta, workload);
          if (inFlightVersion.current.get(cacheKey) !== resourceVersion) {
            // A newer evaluatePhases call for this workload has since been
            // started — this result is stale, discard it.
            return;
          }
          phaseCache.current.set(cacheKey, { resourceVersion, phases });
        } catch {
          // Leave this workload uncached — status stays empty until a
          // future resourceVersion change retries it.
        }
      })
    ).then(() => {
      if (!cancelled) {
        onRows(key, buildRows());
      }
    });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, error]);

  return null;
}
