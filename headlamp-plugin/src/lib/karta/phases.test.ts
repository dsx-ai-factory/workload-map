// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it } from 'vitest';
import {
  KARTA_PHASES,
  phaseItems,
  STATUS_UNAVAILABLE_LABEL,
  statusItems,
  worstPhaseSeverity,
} from './phases';

describe('the phase table', () => {
  it('lists the nine phases worst first, each in its status bucket', () => {
    expect(phaseItems(KARTA_PHASES).map(item => [item.label, item.status])).toEqual([
      ['Failed', 'error'],
      ['Degraded', 'warning'],
      ['Suspending', 'warning'],
      ['Resuming', ''],
      ['Suspended', ''],
      ['Initializing', ''],
      ['Running', 'success'],
      ['Completed', ''],
      ['Undefined', ''],
    ]);
  });

  it('ranks every phase by its place in that order, from 1', () => {
    expect(KARTA_PHASES.map(phase => worstPhaseSeverity([phase]))).toEqual([
      1, 2, 3, 4, 5, 6, 7, 8, 9,
    ]);
  });

  it('puts an unknown phase after every known one in the pills and in the table', () => {
    expect(worstPhaseSeverity(['SomeFuturePhase'])).toBeGreaterThan(
      worstPhaseSeverity(['Undefined'])
    );
    expect(phaseItems(['SomeFuturePhase', 'Undefined']).map(item => item.label)).toEqual([
      'Undefined',
      'SomeFuturePhase',
    ]);
  });
});

describe('statusItems', () => {
  it('is the phases of a workload whose status was evaluated', () => {
    expect(statusItems(['Running', 'Degraded'])).toEqual(phaseItems(['Running', 'Degraded']));
  });

  it('is Unavailable alone, as an error with the reason, when it could not be', () => {
    expect(statusItems(['Running'], 'engine unavailable')).toEqual([
      { label: STATUS_UNAVAILABLE_LABEL, status: 'error', tooltip: 'engine unavailable' },
    ]);
    expect(statusItems([], 'engine unavailable')).toHaveLength(1);
  });
});

describe('phaseItems', () => {
  it('shows a phase once, however often it is repeated', () => {
    expect(
      phaseItems(['Running', 'Running', 'Degraded', 'Running']).map(item => item.label)
    ).toEqual(['Degraded', 'Running']);
  });

  it('orders matched phases worst first', () => {
    expect(phaseItems(['Running', 'Degraded']).map(item => item.label)).toEqual([
      'Degraded',
      'Running',
    ]);
  });

  it('maps phases onto status buckets', () => {
    expect(phaseItems(['Failed', 'Degraded', 'Running', 'Completed'])).toEqual([
      { label: 'Failed', status: 'error' },
      { label: 'Degraded', status: 'warning' },
      { label: 'Running', status: 'success' },
      { label: 'Completed', status: '' },
    ]);
  });

  it('keeps an unrecognized phase, last and neutral', () => {
    expect(phaseItems(['SomeFuturePhase', 'Running'])).toEqual([
      { label: 'Running', status: 'success' },
      { label: 'SomeFuturePhase', status: '' },
    ]);
  });
});

describe('worstPhaseSeverity', () => {
  it('is the severity of the worst phase, unknown phases last', () => {
    expect(worstPhaseSeverity(['Running', 'Failed'])).toBe(1);
    expect(worstPhaseSeverity(['SomeFuturePhase'])).toBe(Number.MAX_SAFE_INTEGER);
    expect(worstPhaseSeverity([])).toBe(Number.MAX_SAFE_INTEGER);
  });
});
