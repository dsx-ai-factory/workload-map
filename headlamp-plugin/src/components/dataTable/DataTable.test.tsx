// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { tableProps } = vi.hoisted(() => ({ tableProps: [] as any[] }));

vi.mock('@kinvolk/headlamp-plugin/lib/CommonComponents', () => ({
  Table: (props: any) => {
    tableProps.push(props);
    return null;
  },
}));

import { DataTable } from './DataTable';

beforeEach(() => {
  tableProps.length = 0;
});

describe('DataTable', () => {
  it('hands the table the function that identifies a row', () => {
    const getRowId = (row: { id: string }) => row.id;
    render(<DataTable id="t" data={[{ id: 'a' }]} columns={[]} getRowId={getRowId} />);

    expect(tableProps.slice(-1)[0].getRowId).toBe(getRowId);
  });

  it('leaves row identity to the table when none is given', () => {
    render(<DataTable id="t" data={[{ id: 'a' }]} columns={[]} />);

    expect(tableProps.slice(-1)[0].getRowId).toBeUndefined();
  });
});
