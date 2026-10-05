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

beforeEach(() => {
  dataTableProps.length = 0;
  clustersRef.current = ['cluster-a'];
});

describe('WorkloadsTable', () => {
  // The age accessor is negated, so ascending is newest first. Sorting it
  // descending would open the table on the oldest workload.
  it('opens sorted newest first', () => {
    render(<WorkloadsTable rows={[]} loading={false} />);

    expect(dataTableProps.at(-1)?.initialSortColumnId).toBe('age');
    expect(dataTableProps.at(-1)?.initialSortDesc).toBe(false);
  });

  it('shows the cluster column only when more than one cluster is selected', () => {
    render(<WorkloadsTable rows={[]} loading={false} />);
    expect(dataTableProps.at(-1)?.columns.some((c: any) => c.id === 'cluster')).toBe(false);

    clustersRef.current = ['cluster-a', 'cluster-b'];
    render(<WorkloadsTable rows={[]} loading={false} />);
    expect(dataTableProps.at(-1)?.columns.some((c: any) => c.id === 'cluster')).toBe(true);
  });
});
