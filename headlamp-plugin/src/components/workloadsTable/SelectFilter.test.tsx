// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MaterialReactTable, useMaterialReactTable } from 'material-react-table';
import { describe, expect, it, vi } from 'vitest';

// Node 24 exposes its own localStorage, which shadows jsdom's and throws
// unless the process was started with a store file. The real CommonComponents
// pulls in Headlamp's redux store, which reads localStorage at import time.
vi.hoisted(() => {
  const storage = new Map<string, string>();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => void storage.set(key, String(value)),
      removeItem: (key: string) => void storage.delete(key),
      clear: () => storage.clear(),
    },
  });
});

import { buildWorkloadColumns } from './columns';

const row = (name: string, namespace: string) => ({
  name,
  namespace,
  cluster: 'local',
  kind: 'Deployment',
  phases: ['Running'],
  creationTimestamp: '2026-01-01T00:00:00Z',
  componentsCount: 2,
});

// Faceted values are left off, as the host does above 500 rows.
function Table({ rows }: { rows: ReturnType<typeof row>[] }) {
  const table = useMaterialReactTable({
    data: rows,
    columns: buildWorkloadColumns(false) as any,
    initialState: { showColumnFilters: true },
  });
  return <MaterialReactTable table={table} />;
}

const shownNames = (container: HTMLElement) =>
  Array.from(container.querySelectorAll('tbody tr')).map(
    row => row.querySelector('td')?.textContent
  );

const openNamespaceMenu = () => {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'Filter by Namespace' }));
  return within(screen.getByRole('listbox'));
};

describe('SelectFilter', () => {
  const rowsA = [row('a', 'team-a'), row('b', 'team-b')];

  it('offers the values of the rows and filters by the picked one', () => {
    const { container } = render(<Table rows={rowsA} />);

    const menu = openNamespaceMenu();
    expect(menu.getAllByRole('option').map(option => option.textContent)).toEqual([
      'team-a',
      'team-b',
    ]);
    fireEvent.click(menu.getByText('team-a'));

    expect(shownNames(container)).toEqual(['a']);
  });

  // The filter value outlives the rows, so after the namespace selector
  // changes the picked value has no rows left. It has to stay in the options,
  // or nothing could uncheck it.
  it('keeps a picked value in the options after its rows are gone', () => {
    const { container, rerender } = render(<Table rows={rowsA} />);
    fireEvent.click(openNamespaceMenu().getByText('team-a'));
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });

    rerender(<Table rows={[row('c', 'team-c')]} />);

    expect(container.textContent).toContain('No results found');
    const menu = openNamespaceMenu();
    expect(menu.getAllByRole('option').map(option => option.textContent)).toEqual([
      'team-a',
      'team-c',
    ]);
    fireEvent.click(menu.getByText('team-a'));
    expect(shownNames(container)).toEqual(['c']);
  });

  // Rows that arrive while the filter row is already open, such as a second
  // cluster loading, are offered the next time the menu opens.
  it('offers values that arrived after the filter row was shown', () => {
    const { rerender } = render(<Table rows={rowsA} />);

    rerender(<Table rows={[...rowsA, row('c', 'team-c')]} />);

    expect(
      openNamespaceMenu()
        .getAllByRole('option')
        .map(option => option.textContent)
    ).toEqual(['team-a', 'team-b', 'team-c']);
  });

  // A cluster-scoped workload has no namespace. It is shown as "-", not as a
  // blank option that gives a blank chip.
  it('lists a workload without a namespace as "-" and filters by it', () => {
    const { container } = render(<Table rows={[row('a', 'team-a'), row('node', '')]} />);

    const menu = openNamespaceMenu();
    expect(menu.getAllByRole('option').map(option => option.textContent)).toEqual(['-', 'team-a']);
    fireEvent.click(menu.getByText('-'));

    expect(shownNames(container)).toEqual(['node']);
  });

  // Joined into one line, many picks run past the column. Each is its own chip
  // instead, which wraps.
  it('shows each picked value as its own chip', () => {
    render(<Table rows={[row('a', 'team-a'), row('b', 'team-b'), row('c', 'team-c')]} />);
    const menu = openNamespaceMenu();
    fireEvent.click(menu.getByText('team-a'));
    fireEvent.click(menu.getByText('team-c'));
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });

    const combobox = screen.getByRole('combobox', { name: 'Filter by Namespace' });

    expect(
      Array.from(combobox.querySelectorAll('.MuiChip-label')).map(chip => chip.textContent)
    ).toEqual(['team-a', 'team-c']);
  });

  // The stock filters have a button that clears the picks in one go.
  it('clears every pick with its clear button, without opening the menu', () => {
    const { container } = render(<Table rows={rowsA} />);
    fireEvent.click(openNamespaceMenu().getByText('team-a'));
    fireEvent.keyDown(screen.getByRole('listbox'), { key: 'Escape' });
    expect(shownNames(container)).toEqual(['a']);

    const combobox = screen.getByRole('combobox', { name: 'Filter by Namespace' });
    const clear = within(combobox.closest('th')!).getByRole('button', { name: 'Clear filter' });
    fireEvent.mouseDown(clear);
    fireEvent.click(clear);

    expect(screen.queryByRole('listbox')).toBeNull();
    expect(shownNames(container)).toEqual(['a', 'b']);
  });
});
