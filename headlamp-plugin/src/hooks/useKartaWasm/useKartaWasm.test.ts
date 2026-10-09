// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { getKartaWasm } = vi.hoisted(() => ({ getKartaWasm: vi.fn() }));
vi.mock('../../lib/karta/karta', () => ({ getKartaWasm }));

import { useKartaWasm } from './useKartaWasm';

describe('useKartaWasm', () => {
  // Without this the call counts carry between tests.
  beforeEach(() => {
    getKartaWasm.mockReset();
  });

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

  // getKartaWasm clears its cached promise on failure, so a new attempt is a
  // genuine retry rather than the old rejection resolving again.
  it('loads again when the attempt changes', async () => {
    getKartaWasm.mockRejectedValueOnce(new Error('wasm load failed'));
    const { result, rerender } = renderHook(({ attempt }) => useKartaWasm(attempt), {
      initialProps: { attempt: 0 },
    });
    await waitFor(() => expect(result.current.error?.message).toBe('wasm load failed'));

    getKartaWasm.mockResolvedValueOnce({ buildTree: vi.fn(), listCatalog: vi.fn() });
    rerender({ attempt: 1 });

    await waitFor(() => expect(result.current.karta).not.toBeNull());
    expect(result.current.error).toBeNull();
    expect(getKartaWasm).toHaveBeenCalledTimes(2);
  });

  it('does not load again while the attempt is unchanged', async () => {
    getKartaWasm.mockResolvedValue({ buildTree: vi.fn(), listCatalog: vi.fn() });
    const { result, rerender } = renderHook(({ attempt }) => useKartaWasm(attempt), {
      initialProps: { attempt: 0 },
    });
    await waitFor(() => expect(result.current.karta).not.toBeNull());

    rerender({ attempt: 0 });

    expect(getKartaWasm).toHaveBeenCalledTimes(1);
  });
});
