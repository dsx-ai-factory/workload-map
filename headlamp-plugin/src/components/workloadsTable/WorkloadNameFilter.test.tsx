// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { act, fireEvent, render, screen, within } from '@testing-library/react';
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

const names = ['gpu-operator', 'kwok-gpu-device-plugin', 'zzz'];
const rows = names.map(name => ({
  name,
  namespace: 'default',
  cluster: 'local',
  kind: 'Deployment',
  phases: ['Running'],
  creationTimestamp: '2026-01-01T00:00:00Z',
  componentsCount: 2,
}));

let currentTable: any;

function Table({ localization }: { localization?: Record<string, string> }) {
  const table = useMaterialReactTable({
    localization: localization as any,
    data: rows,
    columns: buildWorkloadColumns(false) as any,
    initialState: { showColumnFilters: true },
  });
  currentTable = table;
  return <MaterialReactTable table={table} />;
}

const shownNames = (container: HTMLElement) =>
  Array.from(container.querySelectorAll('tbody tr')).map(
    row => row.querySelector('td')?.textContent
  );

describe('WorkloadNameFilter', () => {
  it('filters by what is typed', () => {
    const { container } = render(<Table />);

    fireEvent.change(screen.getByPlaceholderText('Filter by Workload'), {
      target: { value: 'kwok' },
    });

    expect(shownNames(container)).toEqual(['kwok-gpu-device-plugin']);
  });

  // The table's own filter applies typed text after a debounce and does not
  // cancel it on clear, so text typed just before the clear came back.
  it('clears at once, even right after typing', () => {
    vi.useFakeTimers();
    try {
      const { container } = render(<Table />);
      const input = screen.getByPlaceholderText('Filter by Workload') as HTMLInputElement;

      fireEvent.change(input, { target: { value: 'p' } });
      // Within the header cell: the toolbar has a clear-all control of its own.
      fireEvent.click(within(input.closest('th')!).getByRole('button', { name: 'Clear filter' }));
      act(() => {
        vi.advanceTimersByTime(1000);
      });

      expect(input.value).toBe('');
      expect(shownNames(container)).toEqual(names);
    } finally {
      vi.useRealTimers();
    }
  });

  // The stock field named itself from the table's translated strings and left
  // its input where the header filter icon can focus it.
  it('is labelled from the table strings and registered for the header icon', () => {
    render(<Table />);

    const input = screen.getByRole('textbox', { name: 'Filter by Workload' });

    expect(currentTable.refs.filterInputRefs.current['workload-0']).toBe(input);
  });

  it('takes its labels from the table localization', () => {
    render(<Table localization={{ filterByColumn: 'Filtrar por {column}', clearFilter: 'Borrar' }} />);

    const input = screen.getByRole('textbox', { name: 'Filtrar por Workload' });
    fireEvent.change(input, { target: { value: 'kwok' } });

    expect(input.getAttribute('placeholder')).toBe('Filtrar por Workload');
    expect(screen.getAllByLabelText('Borrar').length).toBeGreaterThan(0);
  });
});
