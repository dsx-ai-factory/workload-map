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
  // From discovery: the plural cannot be derived from the kind name, and
  // scope is not in the Karta spec.
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
  // Validated definitions always have a root kind; pkg/catalog and admission
  // both reject one without it.
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

  const [items, error] = ResourceClass.useList({ cluster });

  // Status is a WASM call, so it is recomputed only when resourceVersion
  // changes rather than on every poll.
  const phaseCache = useRef<Map<string, PhaseCacheEntry>>(new Map());
  // The resourceVersion of the most recently started call per workload. Calls
  // can resolve out of order, and a stale result would otherwise overwrite a
  // fresh one until the next poll.
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
            // A newer call for this workload has started; this one is stale.
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, error]);

  return null;
}
