// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { useMemo } from 'react';
import { WorkloadRow } from '../../hooks/useWorkloadRows/workloadRow.types';
import { DataTable } from '../dataTable/DataTable';
import { buildWorkloadColumns, OPTIONAL_COLUMN_IDS } from './columns';

export interface WorkloadsTableProps {
  rows: WorkloadRow[] | null;
  loading?: boolean;
  errorMessage?: string | null;
}

// WorkloadsTable is the unified table across all Karta-described kinds.
export function WorkloadsTable({ rows, loading, errorMessage }: WorkloadsTableProps) {
  const clusters = K8s.useSelectedClusters();
  const showCluster = clusters.length > 1;

  // Joined so the columns, and the open filter menus with them, are rebuilt
  // only when a value appears or disappears, not on every poll.
  const unique = (pick: (row: WorkloadRow) => string) =>
    [...new Set((rows ?? []).map(pick))].sort().join('\n');
  const kinds = unique(row => row.kind);
  const namespaces = unique(row => row.namespace);
  const clusterNames = unique(row => row.cluster);

  const columns = useMemo(
    () =>
      buildWorkloadColumns(showCluster, {
        kinds: kinds ? kinds.split('\n') : [],
        namespaces: namespaces ? namespaces.split('\n') : [],
        clusters: clusterNames ? clusterNames.split('\n') : [],
      }),
    [showCluster, kinds, namespaces, clusterNames]
  );

  return (
    <DataTable
      id="karta-workloads"
      data={rows}
      columns={columns}
      initialSortColumnId="age"
      // The age accessor is negated so that ascending is newest first, which
      // is how Headlamp's own resource tables sort it.
      initialSortDesc={false}
      hiddenColumnIds={[...OPTIONAL_COLUMN_IDS]}
      loading={loading}
      errorMessage={errorMessage}
      emptyMessage="No workloads found."
    />
  );
}
