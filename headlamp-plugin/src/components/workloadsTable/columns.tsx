// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { DateLabel, type TableColumn } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { WorkloadRow } from '../../hooks/useWorkloadRows/workloadRow.types';
import { formatCount, formatCpuMillis, formatMemoryBytes, formatPods } from '../../utils/format';
import {
  KARTA_PHASES,
  StatusPhaseChips,
  worstPhaseSeverity,
} from '../statusPhaseChips/StatusPhaseChips';

// Hidden by default, toggleable through the column picker.
export const OPTIONAL_COLUMN_IDS = [
  'cpuRequest',
  'memoryRequest',
  'components',
  'instances',
  'rawPhase',
] as const;

// includeCluster is false for a single cluster, so the Cluster column is
// absent rather than present and identical on every row.
export function buildWorkloadColumns(includeCluster: boolean): TableColumn<WorkloadRow>[] {
  const columns: TableColumn<WorkloadRow>[] = [
    {
      id: 'workload',
      header: 'Workload',
      accessorFn: row => row.name,
      // Plain text until the detail page lands: a link built now points at
      // nothing.
      gridTemplate: 'auto',
    },
    {
      id: 'type',
      header: 'Type',
      accessorFn: row => row.kind,
      filterVariant: 'multi-select',
      gridTemplate: 'min-content',
    },
    {
      id: 'namespace',
      header: 'Namespace',
      accessorFn: row => row.namespace,
      filterVariant: 'multi-select',
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
      id: 'pods',
      header: 'Pods',
      accessorFn: row => row.podsReady ?? -1,
      filterVariant: 'range',
      Cell: ({ row }) => formatPods(row.original.podsReady, row.original.podsDesired),
      gridTemplate: 'min-content',
    },
    {
      id: 'gpus',
      header: 'GPUs',
      accessorFn: row => row.gpusRequested ?? -1,
      filterVariant: 'range',
      Cell: ({ row }) => formatCount(row.original.gpusRequested),
      gridTemplate: 'min-content',
    },
    {
      id: 'age',
      header: 'Age',
      // Negated so newest sorts first, which makes the raw value useless to
      // filter on: the filter reads the timestamp instead.
      accessorFn: row => -new Date(row.creationTimestamp).getTime(),
      filterVariant: 'date-range',
      filterFn: (row, _columnId, filterValue: [unknown, unknown]) => {
        const [from, to] = filterValue ?? [];
        const created = new Date(row.original.creationTimestamp).getTime();
        if (from && created < new Date(from as string).getTime()) {
          return false;
        }
        // The end of the chosen day, not its midnight, or a workload created
        // during it would be excluded.
        const until = to ? new Date(to as string).setHours(23, 59, 59, 999) : null;
        return !(until && created > until);
      },
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
      gridTemplate: 'min-content',
    });
  }

  columns.push(
    {
      id: 'cpuRequest',
      header: 'CPU request',
      accessorFn: row => row.cpuRequestMillis ?? -1,
      filterVariant: 'range',
      Cell: ({ row }) => formatCpuMillis(row.original.cpuRequestMillis),
      gridTemplate: 'min-content',
    },
    {
      id: 'memoryRequest',
      header: 'Memory request',
      accessorFn: row => row.memoryRequestBytes ?? -1,
      filterVariant: 'range',
      Cell: ({ row }) => formatMemoryBytes(row.original.memoryRequestBytes),
      gridTemplate: 'min-content',
    },
    {
      id: 'components',
      header: 'Components',
      accessorFn: row => row.componentsCount,
      filterVariant: 'range',
      gridTemplate: 'min-content',
    },
    {
      id: 'instances',
      header: 'Instances',
      accessorFn: row => row.instancesCount ?? -1,
      filterVariant: 'range',
      Cell: ({ row }) => formatCount(row.original.instancesCount),
      gridTemplate: 'min-content',
    },
    {
      id: 'rawPhase',
      header: 'Raw phase',
      accessorFn: row => row.rawPhase ?? '',
      filterVariant: 'multi-select',
      Cell: ({ row }) => row.original.rawPhase ?? 'n/a',
      gridTemplate: 'min-content',
    }
  );

  return columns;
}
