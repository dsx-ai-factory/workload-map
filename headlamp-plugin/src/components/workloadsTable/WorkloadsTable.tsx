// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { useMemo } from 'react';
import { WorkloadRow } from '../../hooks/useWorkloadRows';
import { DataTable } from '../dataTable';
import { buildWorkloadColumns, OPTIONAL_COLUMN_IDS } from './columns';

export interface WorkloadsTableProps {
  rows: WorkloadRow[] | null;
  loading?: boolean;
  errorMessage?: string | null;
}

// WorkloadsTable is the unified table across all Karta-described kinds
// (RUN-41607).
export function WorkloadsTable({ rows, loading, errorMessage }: WorkloadsTableProps) {
  const clusters = K8s.useSelectedClusters();
  const showCluster = clusters.length > 1;

  const columns = useMemo(() => buildWorkloadColumns(showCluster), [showCluster]);

  return (
    <DataTable
      id="karta-workloads"
      data={rows}
      columns={columns}
      initialSortColumnId="age"
      hiddenColumnIds={[...OPTIONAL_COLUMN_IDS]}
      loading={loading}
      errorMessage={errorMessage}
      emptyMessage="No workloads found."
    />
  );
}
