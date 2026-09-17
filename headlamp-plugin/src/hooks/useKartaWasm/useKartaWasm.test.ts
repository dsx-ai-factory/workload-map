// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const { getKartaWasm } = vi.hoisted(() => ({ getKartaWasm: vi.fn() }));
vi.mock('../../lib/karta/karta', () => ({ getKartaWasm }));

import { useKartaWasm } from './useKartaWasm';

describe('useKartaWasm', () => {
  it('starts loading, then exposes the resolved module', async () => {
    const karta = { buildTree: vi.fn(), listCatalog: vi.fn() };
    getKartaWasm.mockResolvedValue(karta);

    const { result } = renderHook(() => useKartaWasm());

    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.karta).toBe(karta));
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it('exposes the error when the module fails to load', async () => {
    getKartaWasm.mockRejectedValue(new Error('wasm load failed'));

    const { result } = renderHook(() => useKartaWasm());

    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect(result.current.error?.message).toBe('wasm load failed');
    expect(result.current.karta).toBeNull();
    expect(result.current.loading).toBe(false);
  });
});
