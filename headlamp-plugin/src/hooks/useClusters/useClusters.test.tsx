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

  it('follows the current cluster when nothing is selected', () => {
    currentRef.current = 'a';
    const { result, rerender } = renderHook(() => useClusters());
    expect(result.current).toEqual(['a']);

    currentRef.current = 'b';
    rerender();
    expect(result.current).toEqual(['b']);
  });

  it('switches between the selection and the current cluster as the selection comes and goes', () => {
    currentRef.current = 'a';
    const { result, rerender } = renderHook(() => useClusters());

    selectedRef.current = ['x', 'y'];
    rerender();
    expect(result.current).toEqual(['x', 'y']);

    selectedRef.current = [];
    rerender();
    expect(result.current).toEqual(['a']);
  });

  // Headlamp takes the context from the URL, where %0A is a newline, so a name
  // can hold any character that a separator might be.
  it.each([
    ['a comma', 'a,b'],
    ['a newline', 'west\nblue'],
    ['a tab', 'west\tblue'],
    ['a quote', 'west"blue'],
    ['a bracket pair', '["west","blue"]'],
  ])('keeps a cluster name that contains %s whole', (_what, name) => {
    selectedRef.current = [name, 'c'];
    expect(renderHook(() => useClusters()).result.current).toEqual([name, 'c']);
  });

  it('keeps the current cluster whole too when its name contains a newline', () => {
    currentRef.current = 'west\nblue';
    expect(renderHook(() => useClusters()).result.current).toEqual(['west\nblue']);
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
