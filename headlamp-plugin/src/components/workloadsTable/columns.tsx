// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { DateLabel, type TableColumn } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { WorkloadRow } from '../../hooks/useWorkloadRows/workloadRow.types';
import {
  KARTA_PHASES,
  StatusPhaseChips,
  worstPhaseSeverity,
} from '../statusPhaseChips/StatusPhaseChips';
import { SelectFilter } from './SelectFilter';
import { WorkloadNameFilter } from './WorkloadNameFilter';

// Hidden by default, toggleable through the column picker.
export const OPTIONAL_COLUMN_IDS = ['components'] as const;

// includeCluster is false for a single cluster, so the Cluster column is
// absent rather than present and identical on every row.
export function buildWorkloadColumns(includeCluster: boolean): TableColumn<WorkloadRow>[] {
  const columns: TableColumn<WorkloadRow>[] = [
    {
      id: 'workload',
      header: 'Workload',
      accessorFn: row => row.name,
      Filter: WorkloadNameFilter,
      // Plain text until the detail page lands: a link built now points at
      // nothing.
      gridTemplate: 'auto',
    },
    {
      id: 'type',
      header: 'Type',
      accessorFn: row => row.kind,
      filterVariant: 'multi-select',
      Filter: SelectFilter,
      gridTemplate: 'min-content',
    },
    {
      id: 'namespace',
      header: 'Namespace',
      accessorFn: row => row.namespace,
      filterVariant: 'multi-select',
      Filter: SelectFilter,
      gridTemplate: 'auto',
    },
    {
      id: 'status',
      header: 'Status',
      accessorFn: row => row.phases.join(','),
      // Sorted by the worst phase, so the order matches what the chips show.
      // The accessor joins them, which would otherwise sort alphabetically.
      sortingFn: (a, b) =>
        worstPhaseSeverity(a.original.phases) - worstPhaseSeverity(b.original.phases),
      filterVariant: 'multi-select',
      // The accessor joins the phases, so default options would be
      // combinations such as "Running,Degraded".
      filterSelectOptions: KARTA_PHASES,
      filterFn: (row, _columnId, filterValue: string[]) =>
        filterValue.length === 0 || filterValue.some(phase => row.original.phases.includes(phase)),
      Cell: ({ row }) => <StatusPhaseChips phases={row.original.phases} />,
      gridTemplate: 'auto',
    },
    {
      id: 'age',
      header: 'Age',
      // Negated so newest sorts first.
      accessorFn: row => -new Date(row.creationTimestamp).getTime(),
      // The date-range filter renders MUI X date pickers, which need a
      // LocalizationProvider that Headlamp does not mount, so opening it
      // crashes the page. Headlamp's own Age columns are unfiltered too.
      enableColumnFilter: false,
      Cell: ({ row }) => <DateLabel date={row.original.creationTimestamp} format="mini" />,
      gridTemplate: 'min-content',
    },
  ];

  if (includeCluster) {
    columns.push({
      id: 'cluster',
      header: 'Cluster',
      accessorFn: row => row.cluster,
      filterVariant: 'multi-select',
      Filter: SelectFilter,
      gridTemplate: 'min-content',
    });
  }

  columns.push({
    id: 'components',
    header: 'Components',
    accessorFn: row => row.componentsCount,
    filterVariant: 'range',
    gridTemplate: 'min-content',
  });

  return columns;
}
