// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it } from 'vitest';
import { formatCount, formatCpuMillis, formatMemoryBytes, formatPods } from './format';

describe('formatPods', () => {
  it('formats ready/desired', () => {
    expect(formatPods(2, 3)).toBe('2/3');
  });

  it('falls back to a dash for a missing side', () => {
    expect(formatPods(null, 3)).toBe('-/3');
    expect(formatPods(2, null)).toBe('2/-');
  });

  it('returns n/a when both are unknown', () => {
    expect(formatPods(null, null)).toBe('n/a');
  });
});

describe('formatCount', () => {
  it('formats a known value', () => {
    expect(formatCount(4)).toBe('4');
  });

  it('returns n/a for null', () => {
    expect(formatCount(null)).toBe('n/a');
  });
});

describe('formatCpuMillis', () => {
  it('formats whole cores without the m suffix', () => {
    expect(formatCpuMillis(2000)).toBe('2');
  });

  it('formats fractional cores with the m suffix', () => {
    expect(formatCpuMillis(500)).toBe('500m');
  });

  it('returns n/a for null', () => {
    expect(formatCpuMillis(null)).toBe('n/a');
  });
});

describe('formatMemoryBytes', () => {
  it('formats bytes into the largest whole unit', () => {
    expect(formatMemoryBytes(1024)).toBe('1 KiB');
    expect(formatMemoryBytes(1024 * 1024)).toBe('1 MiB');
    expect(formatMemoryBytes(1536 * 1024 * 1024)).toBe('1.5 GiB');
  });

  it('leaves sub-1024 values in bytes', () => {
    expect(formatMemoryBytes(512)).toBe('512 B');
  });

  // 1023.96 KiB rounds to 1024.0, which has to carry into the next unit
  // rather than render as a value the unit cannot hold.
  it('advances a unit when rounding reaches 1024', () => {
    expect(formatMemoryBytes(1048535)).toBe('1.0 MiB');
    expect(formatMemoryBytes(1073741300)).toBe('1.0 GiB');
  });

  it('returns n/a for null', () => {
    expect(formatMemoryBytes(null)).toBe('n/a');
  });
});
