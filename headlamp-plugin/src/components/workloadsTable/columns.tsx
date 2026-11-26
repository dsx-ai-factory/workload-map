// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { DateLabel, type TableColumn } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { WorkloadRow } from '../../hooks/useWorkloadRows/workloadRow.types';
import {
  KARTA_PHASES,
  STATUS_UNAVAILABLE_LABEL,
  statusItems,
  worstPhaseSeverity,
} from '../../lib/karta/phases';
import { StatusCell } from '../dataTable/cells/StatusCell';
import { clusterColumn, matchesAnySelected } from '../dataTable/columns';
import { SelectFilter } from '../dataTable/SelectFilter';
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
      filterFn: matchesAnySelected,
      gridTemplate: 'min-content',
    },
    {
      id: 'namespace',
      header: 'Namespace',
      // Cluster-scoped workloads have none. A blank value would show as a
      // blank filter option and chip, so the cell and the filter use the same
      // placeholder as Headlamp's own tables.
      accessorFn: row => row.namespace || '-',
      filterVariant: 'multi-select',
      Filter: SelectFilter,
      filterFn: matchesAnySelected,
      gridTemplate: 'auto',
    },
    {
      id: 'status',
      header: 'Status',
      // Headlamp skips cells whose value is unchanged, and phases stay [] across a failure.
      // The label keeps the cell's visible text searchable.
      accessorFn: row =>
        JSON.stringify(
          row.statusError === undefined
            ? row.phases
            : [row.phases, STATUS_UNAVAILABLE_LABEL, row.statusError]
        ),
      // Sorted by the worst phase, so the order matches what the chips show.
      // The accessor joins them, which would otherwise sort alphabetically.
      sortingFn: (a, b) =>
        worstPhaseSeverity(a.original.phases) - worstPhaseSeverity(b.original.phases),
      filterVariant: 'multi-select',
      // The accessor joins the phases, so default options would be
      // combinations such as "Running,Degraded".
      // A workload whose status could not be evaluated shows Unavailable in
      // place of its phases, so that is what it can be found by.
      filterSelectOptions: [...KARTA_PHASES, STATUS_UNAVAILABLE_LABEL],
      filterFn: (row, _columnId, filterValue: string[]) => {
        const shown = statusItems(row.original.phases, row.original.statusError).map(
          item => item.label
        );
        return filterValue.length === 0 || filterValue.some(option => shown.includes(option));
      },
      Cell: ({ row }) => (
        <StatusCell items={statusItems(row.original.phases, row.original.statusError)} />
      ),
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
    columns.push(clusterColumn<WorkloadRow>());
  }

  columns.push({
    id: 'components',
    header: 'Components',
    accessorFn: row => row.componentsCount,
    // The range filter is two text boxes with the table's debounced input, so
    // clearing right after typing leaves the rows filtered under an empty
    // box. Headlamp's own tables do not filter count columns either.
    enableColumnFilter: false,
    gridTemplate: 'min-content',
  });

  return columns;
}
