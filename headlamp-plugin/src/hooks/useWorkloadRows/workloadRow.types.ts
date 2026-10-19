// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// One row of the workloads table: a live workload projected through its Karta
// definition. Not a KubeObject, so ResourceTable cannot render it.
export interface WorkloadRow {
  id: string;
  name: string;
  namespace: string;
  cluster: string;
  kind: string;
  apiGroup: string;
  creationTimestamp: string;

  // Karta-normalized phases, in no guaranteed order.
  phases: string[];

  componentsCount: number;
}
