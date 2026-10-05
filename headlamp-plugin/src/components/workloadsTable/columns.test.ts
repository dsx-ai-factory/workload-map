// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it, vi } from 'vitest';
import { buildWorkloadColumns, OPTIONAL_COLUMN_IDS } from './columns';

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

const DEFAULT_COLUMN_IDS = ['workload', 'type', 'namespace', 'status', 'pods', 'gpus', 'age'];

describe('buildWorkloadColumns', () => {
  it('omits the cluster column when includeCluster is false', () => {
    const columns = buildWorkloadColumns(false);

    expect(columns.map(c => c.id)).toEqual([...DEFAULT_COLUMN_IDS, ...OPTIONAL_COLUMN_IDS]);
  });

  it('includes the cluster column right after the default columns when includeCluster is true', () => {
    const columns = buildWorkloadColumns(true);

    expect(columns.map(c => c.id)).toEqual([...DEFAULT_COLUMN_IDS, 'cluster', ...OPTIONAL_COLUMN_IDS]);
  });

  it('filters type and namespace by picking values rather than typing them', () => {
    const columns = buildWorkloadColumns(true);

    for (const id of ['type', 'namespace']) {
      expect(columns.find(c => c.id === id)?.filterVariant).toBe('multi-select');
    }
  });

  it('every optional column id in OPTIONAL_COLUMN_IDS has a matching column', () => {
    const columns = buildWorkloadColumns(true);
    const ids = new Set(columns.map(c => c.id));

    for (const optionalId of OPTIONAL_COLUMN_IDS) {
      expect(ids.has(optionalId)).toBe(true);
    }
  });

  // Every column filters, each by the control its values suit.
  it('offers every column a filter, picking the control from its values', () => {
    const columns = buildWorkloadColumns(true);
    const variant = (id: string) => columns.find(column => column.id === id)?.filterVariant;

    expect(columns.every(column => column.enableColumnFilter !== false)).toBe(true);
    for (const id of ['type', 'namespace', 'cluster', 'status', 'rawPhase']) {
      expect(variant(id)).toBe('multi-select');
    }
    for (const id of ['pods', 'gpus', 'cpuRequest', 'memoryRequest', 'components', 'instances']) {
      expect(variant(id)).toBe('range');
    }
    expect(variant('age')).toBe('date-range');
    // Names are typed, not picked: there are as many as there are workloads.
    expect(variant('workload')).toBeUndefined();
  });

  // The accessor joins the phases, so the default options would be
  // combinations such as "Running,Degraded" rather than the phases themselves.
  it('filters status by individual phase, not by the joined string', () => {
    const status = buildWorkloadColumns(false).find(column => column.id === 'status')!;
    const match = (phases: string[], selected: string[]) =>
      (status.filterFn as any)({ original: { phases } }, 'status', selected);

    expect(status.filterSelectOptions).toContain('Degraded');
    expect(match(['Running', 'Degraded'], ['Degraded'])).toBe(true);
    expect(match(['Running'], ['Degraded'])).toBe(false);
    // Several selected phases match a workload holding any one of them.
    expect(match(['Running'], ['Degraded', 'Running'])).toBe(true);
    expect(match(['Running'], [])).toBe(true);
  });

  // Age sorts on a negated timestamp so newest comes first, which makes the
  // accessor value meaningless to filter on.
  it('filters age by its timestamp rather than its sort value', () => {
    const age = buildWorkloadColumns(false).find(column => column.id === 'age')!;
    const match = (creationTimestamp: string, range: [unknown, unknown]) =>
      (age.filterFn as any)({ original: { creationTimestamp } }, 'age', range);

    expect(match('2026-06-01T00:00:00Z', ['2026-01-01', '2026-12-31'])).toBe(true);
    expect(match('2025-06-01T00:00:00Z', ['2026-01-01', '2026-12-31'])).toBe(false);
    expect(match('2026-06-01T00:00:00Z', ['2026-07-01', undefined])).toBe(false);
    expect(match('2026-06-01T00:00:00Z', [undefined, undefined])).toBe(true);
  });

  // The accessor is negated so that ascending is newest first. Sorting it
  // descending would put the oldest workload on top.
  it('sorts status by worst phase rather than alphabetically', () => {
    const status = buildWorkloadColumns(false).find(column => column.id === 'status')!;
    const compare = (a: string[], b: string[]) =>
      (status.sortingFn as any)({ original: { phases: a } }, { original: { phases: b } });

    // Failed is worse than Completed, so it sorts first despite C < F.
    expect(compare(['Failed'], ['Completed'])).toBeLessThan(0);
    expect(compare(['Completed'], ['Failed'])).toBeGreaterThan(0);
    // A workload matching several sorts by its worst.
    expect(compare(['Running', 'Degraded'], ['Running'])).toBeLessThan(0);
    // An unrecognised phase sorts last rather than first.
    expect(compare(['Mystery'], ['Undefined'])).toBeGreaterThan(0);
  });

  // `to` is midnight, so comparing against it drops anything created that day.
  it('includes workloads created on the last day of the chosen range', () => {
    const age = buildWorkloadColumns(false).find(column => column.id === 'age')!;
    const match = (creationTimestamp: string, range: [unknown, unknown]) =>
      (age.filterFn as any)({ original: { creationTimestamp } }, 'age', range);

    // Late in the day on purpose: a bound built from local hours lands before
    // this in any timezone west of UTC, and three hours before it here.
    expect(match('2026-06-30T23:30:00Z', ['2026-06-01', '2026-06-30'])).toBe(true);
    expect(match('2026-06-30T00:00:00Z', ['2026-06-01', '2026-06-30'])).toBe(true);
    expect(match('2026-07-01T00:00:01Z', ['2026-06-01', '2026-06-30'])).toBe(false);
  });
});
