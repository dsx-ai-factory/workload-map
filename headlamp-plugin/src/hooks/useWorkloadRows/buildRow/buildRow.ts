// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Definition } from '../../../lib/karta/definitions';
import { Karta, Workload } from '../../../lib/karta/karta.types';
import { WorkloadRow } from '../workloadRow.types';

function componentsCount(karta: Karta): number {
  return 1 + (karta.spec.structureDefinition.childComponents?.length ?? 0);
}

// Projects one workload and its definition into a row. Metadata only: this
// runs on every poll, so the WASM status call is left to KindFetcher.
export function buildWorkloadRow(definition: Definition, workload: Workload, cluster: string): WorkloadRow {
  const karta = definition.karta;
  const kind = karta.spec.structureDefinition.rootComponent.kind;
  const namespace = workload.metadata.namespace ?? '';
  const name = workload.metadata.name;

  return {
    // apiVersion, not group alone: the catalog has DynamoGraphDeployment at
    // v1alpha1 and v1beta1, and a cluster serving both lists it under each.
    id: `${cluster}/${workload.apiVersion}/${namespace}/${workload.kind}/${name}`,
    name,
    namespace,
    cluster,
    kind: workload.kind,
    apiGroup: kind?.group ?? '',
    creationTimestamp: workload.metadata.creationTimestamp ?? '',
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
