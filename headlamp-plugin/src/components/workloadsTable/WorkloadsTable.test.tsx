// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { dataTableProps, clustersRef } = vi.hoisted(() => ({
  dataTableProps: [] as any[],
  clustersRef: { current: ['cluster-a'] as string[] },
}));

vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  K8s: { useSelectedClusters: () => clustersRef.current },
}));
vi.mock('../dataTable/DataTable', () => ({
  DataTable: (props: any) => {
    dataTableProps.push(props);
    return null;
  },
}));

import { WorkloadsTable } from './WorkloadsTable';

// Node 24 exposes its own localStorage, which shadows jsdom's and throws
// unless the process was started with a store file. The real CommonComponents
// pulls in Headlamp's redux store, which reads localStorage at import time
// (plugin/pluginConfigSlice.js), so the stub has to exist before that import.
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

beforeEach(() => {
  dataTableProps.length = 0;
  clustersRef.current = ['cluster-a'];
});

describe('WorkloadsTable', () => {
  // The age accessor is negated, so ascending is newest first. Sorting it
  // descending would open the table on the oldest workload.
  it('opens sorted newest first', () => {
    render(<WorkloadsTable rows={[]} loading={false} />);

    expect(dataTableProps.slice(-1)[0]?.initialSortColumnId).toBe('age');
    expect(dataTableProps.slice(-1)[0]?.initialSortDesc).toBe(false);
  });

  // Cells are keyed by row id, and the default id is the position: when a row
  // above one with an open tooltip disappears, the tooltip would move to the
  // row that took its place.
  it('identifies each row by the workload it shows, not by its position', () => {
    render(<WorkloadsTable rows={[]} loading={false} />);

    const getRowId = dataTableProps.slice(-1)[0]?.getRowId;
    expect(getRowId({ id: 'cluster-a/team/build', name: 'build' })).toBe('cluster-a/team/build');
    expect(getRowId({ id: 'cluster-a/team/serve', name: 'serve' })).toBe('cluster-a/team/serve');
  });

  it('shows the cluster column only when more than one cluster is selected', () => {
    render(<WorkloadsTable rows={[]} loading={false} />);
    expect(dataTableProps.slice(-1)[0]?.columns.some((c: any) => c.id === 'cluster')).toBe(false);

    clustersRef.current = ['cluster-a', 'cluster-b'];
    render(<WorkloadsTable rows={[]} loading={false} />);
    expect(dataTableProps.slice(-1)[0]?.columns.some((c: any) => c.id === 'cluster')).toBe(true);
  });
});
