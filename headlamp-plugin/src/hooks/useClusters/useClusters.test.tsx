// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { selectedRef, currentRef } = vi.hoisted(() => ({
  selectedRef: { current: [] as string[] },
  currentRef: { current: null as string | null },
}));

vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  K8s: { useSelectedClusters: () => selectedRef.current, useCluster: () => currentRef.current },
}));

import { useClusters } from './useClusters';

beforeEach(() => {
  selectedRef.current = [];
  currentRef.current = null;
});

describe('useClusters', () => {
  it('is the explicit selection when there is one', () => {
    selectedRef.current = ['a', 'b'];
    currentRef.current = 'a';
    expect(renderHook(() => useClusters()).result.current).toEqual(['a', 'b']);
  });

  it('falls back to the current cluster when nothing is selected', () => {
    currentRef.current = 'a';
    expect(renderHook(() => useClusters()).result.current).toEqual(['a']);
  });

  it('is empty with neither', () => {
    expect(renderHook(() => useClusters()).result.current).toEqual([]);
  });

  it('keeps a cluster name that contains a comma whole', () => {
    selectedRef.current = ['a,b', 'c'];
    expect(renderHook(() => useClusters()).result.current).toEqual(['a,b', 'c']);
  });

  it('keeps the same array while the selection is unchanged', () => {
    selectedRef.current = ['a', 'b'];
    const { result, rerender } = renderHook(() => useClusters());
    const first = result.current;
    selectedRef.current = ['a', 'b'];
    rerender();
    expect(result.current).toBe(first);
  });
});
