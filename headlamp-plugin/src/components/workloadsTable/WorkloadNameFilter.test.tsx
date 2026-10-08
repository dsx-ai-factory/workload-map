// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { act, fireEvent, render, screen } from '@testing-library/react';
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

function Table() {
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
  it('clears at once, even right after typing', async () => {
    const { container } = render(<Table />);
    const input = screen.getByPlaceholderText('Filter by Workload') as HTMLInputElement;

    fireEvent.change(input, { target: { value: 'p' } });
    fireEvent.click(screen.getAllByLabelText('Clear filter')[0]);
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 600));
    });

    expect(input.value).toBe('');
    expect(shownNames(container)).toEqual(names);
  });
});
