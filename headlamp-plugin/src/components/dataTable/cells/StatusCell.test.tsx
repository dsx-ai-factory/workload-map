// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@kinvolk/headlamp-plugin/lib/CommonComponents', () => ({
  StatusLabel: ({ status, children }: any) => <span data-status={status}>{children}</span>,
}));

import { StatusCell } from './StatusCell';

describe('StatusCell', () => {
  it('renders one pill per item, in the given order', () => {
    render(
      <StatusCell
        items={[
          { label: 'Degraded', status: 'warning' },
          { label: 'Running', status: 'success' },
        ]}
      />
    );
    const labels = screen.getAllByText(/Running|Degraded/).map(el => el.textContent);
    expect(labels).toEqual(['Degraded', 'Running']);
    expect(screen.getByText('Running').getAttribute('data-status')).toBe('success');
  });

  it('shows the tooltip of an item that has one, and only that one', () => {
    render(
      <StatusCell
        items={[
          { label: 'Not Ready', status: 'error', tooltip: 'CRDMissing: no CRD' },
          { label: 'Running', status: 'success' },
        ]}
      />
    );
    expect(screen.getByLabelText('Not Ready: CRDMissing: no CRD')).toBeTruthy();
    expect(screen.queryAllByRole('tooltip')).toHaveLength(0);
    expect(screen.getAllByLabelText(/.+/)).toHaveLength(1);
  });

  it('lets the keyboard reach an explained pill, and only that one', () => {
    render(
      <StatusCell
        items={[
          { label: 'Not Ready', status: 'error', tooltip: 'CRDMissing: no CRD' },
          { label: 'Running', status: 'success' },
        ]}
      />
    );
    const explained = screen.getByLabelText('Not Ready: CRDMissing: no CRD');
    expect(explained.getAttribute('tabindex')).toBe('0');
    expect(screen.getByText('Running').closest('[tabindex]')).toBeNull();
  });

  it('renders an item with an unknown status without crashing', () => {
    render(<StatusCell items={[{ label: 'Whatever', status: '' }]} />);
    expect(screen.getByText('Whatever')).toBeTruthy();
  });
});
