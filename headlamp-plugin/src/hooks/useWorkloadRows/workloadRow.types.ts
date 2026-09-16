// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// One row of the unified workloads table (RUN-41607). Built from a live
// workload instance plus its Karta-computed status/pods/resources — not a
// raw Kubernetes object, so it's a plain shape rather than a KubeObject
// subclass (ResourceTable/ResourceListView require the latter; this table
// is built on the lower-level Table component instead — see WorkloadsTable).
export interface WorkloadRow {
  id: string;
  name: string;
  namespace: string;
  cluster: string;
  kind: string;
  apiGroup: string;
  creationTimestamp: string;
  detailPath: string;

  // Karta-normalized status phases (severity order not guaranteed here —
  // see columns.tsx's renderPhases for display ordering).
  phases: string[];

  // null means "not yet computed" (e.g. AttributePods/RollupReadyCounts
  // haven't landed yet — see the RUN-41607 design plan's open gaps).
  podsReady: number | null;
  podsDesired: number | null;
  gpusRequested: number | null;

  // Optional (P1) columns, hidden by default behind the column picker.
  cpuRequestMillis: number | null;
  memoryRequestBytes: number | null;
  componentsCount: number;
  instancesCount: number | null;
  // The kind's own status value before Karta normalization.
  rawPhase: string | null;
}
