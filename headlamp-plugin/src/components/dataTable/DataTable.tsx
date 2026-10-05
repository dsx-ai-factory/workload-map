// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { CommonComponents } from '@kinvolk/headlamp-plugin/lib';
import type { TableColumn } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { ReactNode, useMemo } from 'react';

const { Table } = CommonComponents;

export interface DataTableProps<RowItem extends Record<string, any>> {
  // Must be unique among tables on a page: it keys the URL-reflected state.
  id: string;
  data: RowItem[] | null;
  columns: TableColumn<RowItem>[];
  initialSortColumnId?: string;
  initialSortDesc?: boolean;
  // Start hidden but stay toggleable, unlike Table's own hideColumns.
  hiddenColumnIds?: string[];
  loading?: boolean;
  errorMessage?: string | null;
  emptyMessage?: ReactNode;
}

// Wraps Headlamp's Table with the sorting and column setup every page needs.
// Not ResourceTable, which requires rows to extend KubeObject.
export function DataTable<RowItem extends Record<string, any>>({
  id,
  data,
  columns,
  initialSortColumnId,
  initialSortDesc = true,
  hiddenColumnIds,
  loading,
  errorMessage,
  emptyMessage,
}: DataTableProps<RowItem>) {
  const initialState = useMemo(() => {
    const state: { sorting?: { id: string; desc: boolean }[]; columnVisibility?: Record<string, boolean> } = {};
    if (initialSortColumnId) {
      state.sorting = [{ id: initialSortColumnId, desc: initialSortDesc }];
    }
    if (hiddenColumnIds?.length) {
      state.columnVisibility = Object.fromEntries(hiddenColumnIds.map(colId => [colId, false]));
    }
    return state;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialSortColumnId, initialSortDesc, hiddenColumnIds?.join(',')]);

  // Gated on having nothing to show, so a refresh never replaces populated
  // rows with a spinner.
  const showLoader = !!loading && !data?.length;

  return (
    <Table
      data={data ?? []}
      columns={columns}
      loading={showLoader}
      errorMessage={errorMessage ?? undefined}
      emptyMessage={emptyMessage}
      initialState={initialState}
      reflectInURL={id}
    />
  );
}
