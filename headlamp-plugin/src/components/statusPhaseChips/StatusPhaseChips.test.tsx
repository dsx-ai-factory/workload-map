// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { StatusPhaseChips } from './StatusPhaseChips';

describe('StatusPhaseChips', () => {
  it('renders one chip per phase', () => {
    render(<StatusPhaseChips phases={['Running']} />);

    expect(screen.getByText('Running')).toBeTruthy();
  });

  it('renders multiple matched phases worst-severity first', () => {
    render(<StatusPhaseChips phases={['Running', 'Degraded']} />);

    const labels = screen.getAllByText(/Running|Degraded/).map(el => el.textContent);
    expect(labels).toEqual(['Degraded', 'Running']);
  });

  it('renders an unrecognized phase without crashing', () => {
    render(<StatusPhaseChips phases={['SomeFuturePhase']} />);

    expect(screen.getByText('SomeFuturePhase')).toBeTruthy();
  });
});
