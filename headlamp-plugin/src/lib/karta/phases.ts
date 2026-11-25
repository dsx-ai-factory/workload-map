// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import type { StatusItem } from './statusItem';

// The 9 normalized Karta phases, worst first, each with the StatusLabel bucket
// it maps onto. One table drives the order, the buckets and the filter options,
// so a phase cannot be added to one of them and missed in the others.
const PHASES = [
  ['Failed', 'error'],
  ['Degraded', 'warning'],
  ['Suspending', 'warning'],
  ['Resuming', ''],
  ['Suspended', ''],
  ['Initializing', ''],
  ['Running', 'success'],
  ['Completed', ''],
  ['Undefined', ''],
] as const satisfies readonly (readonly [string, StatusItem['status']])[];

// Shown in place of the phases when they could not be evaluated.
export const STATUS_UNAVAILABLE_LABEL = 'Unavailable';

// Worst first, so a filter offers them in the order the pills use.
export const KARTA_PHASES: readonly string[] = PHASES.map(([phase]) => phase);

// 1 for the worst phase. A phase this build does not know sorts after every
// known one, for the pills and for the table alike.
function phaseSeverity(phase: string): number {
  const index = KARTA_PHASES.indexOf(phase);
  return index === -1 ? Number.MAX_SAFE_INTEGER : index + 1;
}

// The severity of a workload's worst phase, which is the one its pills lead
// with.
export function worstPhaseSeverity(phases: string[]): number {
  return Math.min(...phases.map(phaseSeverity), Number.MAX_SAFE_INTEGER);
}

// A workload can match several phases at once, such as Running and Degraded.
// Each shows once, however often the engine repeats it.
export function phaseItems(phases: readonly string[]): StatusItem[] {
  return [...new Set(phases)]
    .sort((a, b) => phaseSeverity(a) - phaseSeverity(b))
    .map(phase => ({ label: phase, status: PHASES.find(([name]) => name === phase)?.[1] ?? '' }));
}
