// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import type { TableColumn } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { SelectFilter } from './SelectFilter';

// Matches a row whose value is exactly one of the selected ones. The table's
// own multi-select match is a substring test, so picking "team" would also
// keep "team-b", and picking "-" would keep every hyphenated namespace.
export function matchesAnySelected(
  row: { getValue: (columnId: string) => unknown },
  columnId: string,
  selected: string[]
) {
  return selected.includes(row.getValue(columnId) as string);
}
// An emptied selection removes the filter, so the header stops showing one.
matchesAnySelected.autoRemove = (selected?: string[]) => !selected?.length;

export function clusterColumn<Row extends { cluster: string }>(): TableColumn<Row> {
  return {
    id: 'cluster',
    header: 'Cluster',
    accessorFn: row => row.cluster,
    filterVariant: 'multi-select',
    Filter: SelectFilter,
    filterFn: matchesAnySelected,
    gridTemplate: 'min-content',
  };
}
