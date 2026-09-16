// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { CommonComponents } from '@kinvolk/headlamp-plugin/lib';
import type { TableColumn } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import { ReactNode, useMemo } from 'react';

const { Table } = CommonComponents;

export interface DataTableProps<RowItem extends Record<string, any>> {
  // Identifies this table for URL-reflected page/sort state — must be
  // unique among tables rendered on the same page.
  id: string;
  data: RowItem[] | null;
  columns: TableColumn<RowItem>[];
  initialSortColumnId?: string;
  initialSortDesc?: boolean;
  // Column ids to start hidden — still toggleable via the table's built-in
  // column picker, unlike Table's own hideColumns (permanently hidden).
  hiddenColumnIds?: string[];
  loading?: boolean;
  errorMessage?: string | null;
  emptyMessage?: ReactNode;
}

// DataTable wraps Headlamp's Table (CommonComponents) with the sorting/
// hideable-column/URL-reflection setup every page's table needs, so pages
// don't each re-derive that boilerplate. Not ResourceTable/ResourceListView:
// those require rows to extend KubeObject, which Karta-computed rows aren't.
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

  // Table renders its `loading` branch as a bare spinner, but renders
  // `emptyMessage` inside an outlined Paper — a panel the spinner does not
  // need. Both branches return before the toolbar and headers either way, so
  // forwarding `loading` costs no chrome. It is gated on having nothing to
  // show so that a refresh never replaces populated rows with a spinner.
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
