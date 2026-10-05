// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { useWorkloadRows, workloadsTableProps } = vi.hoisted(() => ({
  useWorkloadRows: vi.fn(),
  workloadsTableProps: [] as any[],
}));

vi.mock('@kinvolk/headlamp-plugin/lib/CommonComponents', () => ({
  SectionBox: ({ children }: any) => <div>{children}</div>,
  SectionFilterHeader: ({ title }: any) => <h1>{title}</h1>,
}));
vi.mock('../../hooks/useWorkloadRows/useWorkloadRows', () => ({ useWorkloadRows }));
vi.mock('../../components/workloadsTable/WorkloadsTable', () => ({
  WorkloadsTable: (props: any) => {
    workloadsTableProps.push(props);
    return <div data-testid="table" />;
  },
}));

import { WorkloadsPage } from './WorkloadsPage';

function givenRows(result: Partial<ReturnType<typeof useWorkloadRows>> = {}) {
  useWorkloadRows.mockReturnValue({
    rows: [],
    loading: false,
    error: null,
    engineError: null,
    errorsByKind: {},
    errorsByCluster: {},
    warningsByCluster: {},
    fetchers: null,
    ...result,
  });
}

beforeEach(() => {
  workloadsTableProps.length = 0;
  useWorkloadRows.mockReset();
});

describe('WorkloadsPage', () => {
  it('passes the rows and loading state to the table', () => {
    givenRows({ rows: [{ id: 'local/api', name: 'api' }] as any, loading: false });

    render(<WorkloadsPage />);

    expect(workloadsTableProps.at(-1)?.rows).toHaveLength(1);
    expect(workloadsTableProps.at(-1)?.errorMessage).toBeUndefined();
  });

  // Headlamp's Table hides every row whenever errorMessage is set, so only a
  // failure that leaves nothing to show belongs there.
  it('gives the table an error message only when nothing could be fetched', () => {
    givenRows({ rows: null, error: new Error('every cluster unreachable') });

    render(<WorkloadsPage />);

    expect(workloadsTableProps.at(-1)?.errorMessage).toBe('every cluster unreachable');
  });

  // Names, namespaces and ages are metadata: only the status column needs the
  // engine, so its failure belongs beside the rows.
  it('reports a failed engine beside the rows rather than hiding them', () => {
    givenRows({
      rows: [{ id: 'local/api', name: 'api' }] as any,
      engineError: new Error('wasm load failed'),
    });

    render(<WorkloadsPage />);

    expect(screen.getByText(/Status unavailable: wasm load failed/)).toBeDefined();
    expect(workloadsTableProps.at(-1)?.errorMessage).toBeUndefined();
    expect(workloadsTableProps.at(-1)?.rows).toHaveLength(1);
  });

  it('reports a failing kind beside the table rather than in place of it', () => {
    givenRows({
      rows: [{ id: 'local/api', name: 'api' }] as any,
      errorsByKind: { 'cluster-a/rayjob': new Error('rayjobs is forbidden') },
    });

    render(<WorkloadsPage />);

    expect(screen.getByText(/Unable to list cluster-a\/rayjob/)).toBeDefined();
    expect(screen.getByTestId('table')).toBeDefined();
    expect(workloadsTableProps.at(-1)?.errorMessage).toBeUndefined();
  });

  // A cluster that still produced rows explains nothing about a kind that
  // failed under it.
  it('reports a kind failure under a cluster that only warned', () => {
    givenRows({
      rows: [{ id: 'cluster-a/api', name: 'api' }] as any,
      warningsByCluster: { 'cluster-a': new Error('kartas forbidden') },
      errorsByKind: { 'cluster-a/rayjob': new Error('rayjobs is forbidden') },
    });

    render(<WorkloadsPage />);

    expect(screen.getByText(/Partial results for cluster cluster-a/)).toBeDefined();
    expect(screen.getByText(/Unable to list cluster-a\/rayjob/)).toBeDefined();
  });

  // An unreachable cluster explains every kind missing under it, so repeating
  // the message per kind would bury the one line that matters.
  it('reports an unreachable cluster once, not once per kind beneath it', () => {
    givenRows({
      rows: [{ id: 'cluster-b/api', name: 'api' }] as any,
      errorsByCluster: { 'cluster-a': new Error('unreachable') },
      errorsByKind: {
        'cluster-a/deployment': new Error('unreachable'),
        'cluster-b/rayjob': new Error('rayjobs is forbidden'),
      },
    });

    render(<WorkloadsPage />);

    expect(screen.getByText(/Unable to read cluster cluster-a/)).toBeDefined();
    expect(screen.queryByText(/cluster-a\/deployment/)).toBeNull();
    // A kind failing on a cluster that is otherwise fine still reports.
    expect(screen.getByText(/Unable to list cluster-b\/rayjob/)).toBeDefined();
  });
});
