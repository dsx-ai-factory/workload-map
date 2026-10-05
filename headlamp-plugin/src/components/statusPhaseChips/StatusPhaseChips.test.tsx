// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { StatusPhaseChips } from './StatusPhaseChips';

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
