// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { act, render } from '@testing-library/react';
import { MaterialReactTable, useMaterialReactTable } from 'material-react-table';
import { createElement } from 'react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('./SelectFilter', () => ({ SelectFilter: () => null }));

import { clusterColumn, matchesAnySelected } from './columns';
import { SelectFilter } from './SelectFilter';

describe('matchesAnySelected', () => {
  const row = { getValue: () => 'team-b' };

  it('matches exactly, not by substring', () => {
    expect(matchesAnySelected(row, 'c', ['team'])).toBe(false);
    expect(matchesAnySelected(row, 'c', ['team-b'])).toBe(true);
  });

  it('matches a row whose value is any one of several selected values', () => {
    expect(matchesAnySelected({ getValue: () => 'c2' }, 'cluster', ['c1', 'c2'])).toBe(true);
    expect(matchesAnySelected({ getValue: () => 'c3' }, 'cluster', ['c1', 'c2'])).toBe(false);
  });

  it('does not match a value that is missing or not a string', () => {
    expect(matchesAnySelected({ getValue: () => undefined }, 'cluster', ['c1'])).toBe(false);
    expect(matchesAnySelected({ getValue: () => 2 }, 'cluster', ['2'])).toBe(false);
  });

  it('removes the filter when the selection is empty', () => {
    expect(matchesAnySelected.autoRemove([])).toBe(true);
    expect(matchesAnySelected.autoRemove(['a'])).toBe(false);
  });
});

describe('clusterColumn', () => {
  it('reads the row cluster and filters it by exact selection', () => {
    const column = clusterColumn<{ cluster: string }>();
    expect(column.id).toBe('cluster');
    expect((column.accessorFn as any)({ cluster: 'c1' })).toBe('c1');
    expect(column.filterFn).toBe(matchesAnySelected);
  });

  it('is a multi-select column with the shared filter component', () => {
    const column = clusterColumn<{ cluster: string }>();
    expect(column.header).toBe('Cluster');
    expect(column.filterVariant).toBe('multi-select');
    expect(column.Filter).toBe(SelectFilter);
    expect(column.gridTemplate).toBe('min-content');
  });

  describe('in a table', () => {
    const rows = [
      { name: 'a', cluster: 'c1' },
      { name: 'b', cluster: 'c2' },
      { name: 'c', cluster: 'c3' },
      { name: 'd', cluster: 'c2-b' },
    ];

    let tableInstance: any;

    function Table({ selected }: { selected: string[] }) {
      const table = useMaterialReactTable({
        data: rows,
        columns: [
          { id: 'name', header: 'Name', accessorFn: row => row.name },
          clusterColumn<(typeof rows)[number]>() as any,
        ],
        initialState: { columnFilters: [{ id: 'cluster', value: selected }] },
      });
      tableInstance = table;
      return createElement(MaterialReactTable, { table: table as any });
    }

    const shownNames = (container: HTMLElement) =>
      Array.from(container.querySelectorAll('tbody tr')).map(
        row => row.querySelector('td')?.textContent
      );

    it('keeps exactly the rows of the selected clusters, not near matches', () => {
      const { container } = render(createElement(Table, { selected: ['c2', 'c3'] }));
      expect(shownNames(container)).toEqual(['b', 'c']);
    });

    // Initial state is applied as given, so the emptied selection has to come
    // through the table's own setter, which is where autoRemove is consulted.
    it('shows every row once the selection is emptied', () => {
      const { container } = render(createElement(Table, { selected: ['c2'] }));
      expect(shownNames(container)).toEqual(['b']);

      act(() => tableInstance.getColumn('cluster').setFilterValue([]));
      expect(shownNames(container)).toEqual(['a', 'b', 'c', 'd']);
    });
  });
});
