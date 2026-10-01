// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { useEffect, useMemo, useRef } from 'react';
import { Definition } from '../../../lib/karta/definitions';
import { Workload } from '../../../lib/karta/karta.types';
import { evaluatePhases } from '../../../lib/karta/kartaUtil';
import { buildWorkloadRow } from '../buildRow/buildRow';
import { WorkloadRow } from '../workloadRow.types';

export interface KindFetcherProps {
  definition: Definition;
  // useList() spans every selected cluster when given none, which would
  // evaluate another cluster's workloads against this one's definitions.
  cluster: string;
  // From discovery: neither is derivable from the kind name or the spec.
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

// One per definition: the kinds are known only at runtime, so each needs its
// own useList(), which the Rules of Hooks forbid in a loop. Renders nothing.
export function KindFetcher({
  definition,
  cluster,
  plural,
  namespaced,
  onRows,
  onError,
}: KindFetcherProps) {
  const key = definition.karta.metadata.name;
  // Validated definitions always have a root kind.
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

  // With allowed namespaces, useList queries per namespace and returns the
  // ones that resolved while the rest are pending. isLoading says they all are.
  const listResult = ResourceClass.useList({ cluster });
  const [items, error] = listResult;
  const listLoading = listResult.isLoading;

  // Status is a WASM call, recomputed only when resourceVersion changes.
  const phaseCache = useRef<Map<string, PhaseCacheEntry>>(new Map());
  // The version of the most recently started call per workload: calls can
  // resolve out of order, and a stale one would overwrite a fresh result.
  const inFlightVersion = useRef<Map<string, string>>(new Map());

  // The definition's version, not its identity: useKartaDefinitions rebuilds
  // the objects every render. The ref keeps identity out of the effect deps.
  const definitionVersion = definition.karta.metadata.resourceVersion ?? '';
  const definitionRef = useRef(definition);
  definitionRef.current = definition;

  // A changed definition computes different phases from the same workload.
  useEffect(() => {
    phaseCache.current.clear();
    inFlightVersion.current.clear();
  }, [definitionVersion]);

  useEffect(() => {
    if (error) {
      onError(key, new Error(error.message));
      return;
    }
    if (listLoading || !items) {
      return;
    }

    // Pinned per run, so a mid-flight change cannot mix the two definitions.
    const currentDefinition = definitionRef.current;

    // Otherwise a page left open through job churn holds every uid it saw.
    const live = new Set(items.map(item => workloadCacheKey(item.jsonData as Workload)));
    for (const cache of [phaseCache.current, inFlightVersion.current]) {
      for (const cached of cache.keys()) {
        if (!live.has(cached)) {
          cache.delete(cached);
        }
      }
    }

    const buildRows = () =>
      items.map(item => {
        const workload = item.jsonData as Workload;
        const row = buildWorkloadRow(currentDefinition, workload, item.cluster);
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
          const phases = await evaluatePhases(currentDefinition.karta, workload);
          if (cancelled || inFlightVersion.current.get(cacheKey) !== resourceVersion) {
            // A newer call or definition has started; this result is stale.
            return;
          }
          phaseCache.current.set(cacheKey, { resourceVersion, phases });
        } catch {
          // Leave uncached; a later resourceVersion change retries.
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
  }, [items, error, listLoading, definitionVersion, key, onRows, onError]);

  return null;
}
