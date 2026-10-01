// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Definition } from '../../../lib/karta/definitions';
import { Karta, Workload } from '../../../lib/karta/karta.types';
import { WorkloadRow } from '../workloadRow.types';

function detailPath(kind: { group: string; version: string; kind: string }, namespace: string, name: string): string {
  return `/karta/workloads/${kind.group || 'core'}/${kind.version}/${kind.kind}/${namespace}/${name}`;
}

function componentsCount(karta: Karta): number {
  return 1 + (karta.spec.structureDefinition.childComponents?.length ?? 0);
}

// Projects one live workload and its definition into a row. Metadata only:
// this runs on every list poll for every instance, so the WASM status call is
// left to KindFetcher, which caches it.
export function buildWorkloadRow(definition: Definition, workload: Workload, cluster: string): WorkloadRow {
  const karta = definition.karta;
  const kind = karta.spec.structureDefinition.rootComponent.kind;
  const namespace = workload.metadata.namespace ?? '';
  const name = workload.metadata.name;

  return {
    id: `${cluster}/${namespace}/${workload.kind}/${name}`,
    name,
    namespace,
    cluster,
    kind: workload.kind,
    apiGroup: kind?.group ?? '',
    creationTimestamp: workload.metadata.creationTimestamp ?? '',
    detailPath: kind ? detailPath(kind, namespace, name) : `/karta/workloads/${namespace}/${name}`,
    phases: [],
    podsReady: null,
    podsDesired: null,
    gpusRequested: null,
    cpuRequestMillis: null,
    memoryRequestBytes: null,
    componentsCount: componentsCount(karta),
    instancesCount: null,
    rawPhase: null,
  };
}
