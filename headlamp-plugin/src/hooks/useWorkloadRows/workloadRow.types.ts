// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// One row of the unified workloads table: a live workload projected through
// its Karta definition. Not a KubeObject subclass, so ResourceTable and
// ResourceListView cannot render it.
export interface WorkloadRow {
  id: string;
  name: string;
  namespace: string;
  cluster: string;
  kind: string;
  apiGroup: string;
  creationTimestamp: string;
  detailPath: string;

  // Karta-normalized phases, in no guaranteed order.
  phases: string[];

  // null until pod attribution and ready-count rollup exist.
  podsReady: number | null;
  podsDesired: number | null;
  gpusRequested: number | null;

  cpuRequestMillis: number | null;
  memoryRequestBytes: number | null;
  componentsCount: number;
  instancesCount: number | null;
  // The kind's own status value before Karta normalization.
  rawPhase: string | null;
}
