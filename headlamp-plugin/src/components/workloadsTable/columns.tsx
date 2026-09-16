// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { CommonComponents } from '@kinvolk/headlamp-plugin/lib';
import type { TableColumn } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import MuiLink from '@mui/material/Link';
import { Link as RouterLink } from 'react-router-dom';
import { WorkloadRow } from '../../hooks/useWorkloadRows';
import { formatCount, formatCpuMillis, formatMemoryBytes, formatPods } from '../../utils/format';
import { StatusPhaseChips } from '../statusPhaseChips';

const { DateLabel } = CommonComponents;

// Column ids optional (P1) columns use, so WorkloadsTable can mark them
// hidden-by-default in the table's initial column-visibility state while
// still letting the column picker toggle them on.
export const OPTIONAL_COLUMN_IDS = [
  'cpuRequest',
  'memoryRequest',
  'components',
  'instances',
  'rawPhase',
] as const;

// buildWorkloadColumns returns the default (P0) columns plus the optional
// (P1) ones — WorkloadsTable hides the latter initially via
// OPTIONAL_COLUMN_IDS, the table's built-in column picker toggles them.
// includeCluster is false when a single cluster is selected, so the Cluster
// column isn't shown at all rather than shown-and-empty.
export function buildWorkloadColumns(includeCluster: boolean): TableColumn<WorkloadRow>[] {
  const columns: TableColumn<WorkloadRow>[] = [
    {
      id: 'workload',
      header: 'Workload',
      accessorFn: row => row.name,
      Cell: ({ row }) => (
        <MuiLink component={RouterLink} to={row.original.detailPath}>
          {row.original.name}
        </MuiLink>
      ),
      gridTemplate: 'auto',
    },
    {
      id: 'type',
      header: 'Type',
      accessorFn: row => row.kind,
      gridTemplate: 'min-content',
    },
    {
      id: 'namespace',
      header: 'Namespace',
      accessorFn: row => row.namespace,
      gridTemplate: 'auto',
    },
    {
      id: 'status',
      header: 'Status',
      accessorFn: row => row.phases.join(','),
      Cell: ({ row }) => <StatusPhaseChips phases={row.original.phases} />,
      gridTemplate: 'auto',
    },
    {
      id: 'pods',
      header: 'Pods',
      accessorFn: row => row.podsReady ?? -1,
      Cell: ({ row }) => formatPods(row.original.podsReady, row.original.podsDesired),
      gridTemplate: 'min-content',
    },
    {
      id: 'gpus',
      header: 'GPUs',
      accessorFn: row => row.gpusRequested ?? -1,
      Cell: ({ row }) => formatCount(row.original.gpusRequested),
      gridTemplate: 'min-content',
    },
    {
      id: 'age',
      header: 'Age',
      accessorFn: row => -new Date(row.creationTimestamp).getTime(),
      Cell: ({ row }) => <DateLabel date={row.original.creationTimestamp} format="mini" />,
      gridTemplate: 'min-content',
    },
  ];

  if (includeCluster) {
    columns.push({
      id: 'cluster',
      header: 'Cluster',
      accessorFn: row => row.cluster,
      gridTemplate: 'min-content',
    });
  }

  columns.push(
    {
      id: 'cpuRequest',
      header: 'CPU request',
      accessorFn: row => row.cpuRequestMillis ?? -1,
      Cell: ({ row }) => formatCpuMillis(row.original.cpuRequestMillis),
      gridTemplate: 'min-content',
    },
    {
      id: 'memoryRequest',
      header: 'Memory request',
      accessorFn: row => row.memoryRequestBytes ?? -1,
      Cell: ({ row }) => formatMemoryBytes(row.original.memoryRequestBytes),
      gridTemplate: 'min-content',
    },
    {
      id: 'components',
      header: 'Components',
      accessorFn: row => row.componentsCount,
      gridTemplate: 'min-content',
    },
    {
      id: 'instances',
      header: 'Instances',
      accessorFn: row => row.instancesCount ?? -1,
      Cell: ({ row }) => formatCount(row.original.instancesCount),
      gridTemplate: 'min-content',
    },
    {
      id: 'rawPhase',
      header: 'Raw phase',
      accessorFn: row => row.rawPhase ?? '',
      Cell: ({ row }) => row.original.rawPhase ?? 'n/a',
      gridTemplate: 'min-content',
    }
  );

  return columns;
}
