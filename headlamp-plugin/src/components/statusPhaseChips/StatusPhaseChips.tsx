// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Icon } from '@iconify/react';
import { StatusLabel } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import Stack from '@mui/material/Stack';

// The 9 normalized Karta phases, worst first.
const PHASE_SEVERITY: Record<string, number> = {
  Failed: 1,
  Degraded: 2,
  Suspending: 3,
  Resuming: 4,
  Suspended: 5,
  Initializing: 6,
  Running: 7,
  Completed: 8,
  Undefined: 9,
};

// Worst first, so a filter offers them in the order the chips use.
export const KARTA_PHASES = Object.keys(PHASE_SEVERITY);

// The severity of a workload's worst phase, which is the one its chips lead
// with. Unknown phases sort last.
export function worstPhaseSeverity(phases: string[]): number {
  return Math.min(
    ...phases.map(phase => PHASE_SEVERITY[phase] ?? Number.MAX_SAFE_INTEGER),
    Number.MAX_SAFE_INTEGER
  );
}

// StatusLabel understands only four buckets, the same ones Headlamp's own
// workload views use, so the 9 phases map onto them by severity.
const PHASE_STATUS: Record<string, 'success' | 'warning' | 'error' | ''> = {
  Failed: 'error',
  Degraded: 'warning',
  Suspending: 'warning',
  Resuming: '',
  Suspended: '',
  Initializing: '',
  Running: 'success',
  Completed: '',
  Undefined: '',
};

export interface StatusPhaseChipsProps {
  phases: string[];
}

// A pill per matched phase, worst first. A workload can match several at once,
// such as Running and Degraded.
export function StatusPhaseChips({ phases }: StatusPhaseChipsProps) {
  const sorted = [...phases].sort((a, b) => (PHASE_SEVERITY[a] ?? 99) - (PHASE_SEVERITY[b] ?? 99));
  return (
    <Stack direction="row" spacing={1} flexWrap="wrap" alignItems="center">
      {sorted.map(phase => {
        const status = PHASE_STATUS[phase] ?? '';
        return (
          <StatusLabel key={phase} status={status}>
            {(status === 'warning' || status === 'error') && (
              <Icon aria-hidden icon="mdi:alert-outline" width="1.2rem" height="1.2rem" />
            )}
            {phase}
          </StatusLabel>
        );
      })}
    </Stack>
  );
}
