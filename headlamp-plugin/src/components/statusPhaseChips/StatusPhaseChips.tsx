// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Icon } from '@iconify/react';
import { CommonComponents } from '@kinvolk/headlamp-plugin/lib';
import Box from '@mui/material/Box';
import Stack from '@mui/material/Stack';

const { StatusLabel } = CommonComponents;

// Mirrors the 9 normalized Karta phases and their severity order (worst
// first) from the design doc's status appendix.
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

// StatusLabel only understands success/warning/error/'' (Headlamp's Pod and
// workload list views use the same four buckets) — mapped from the 9 Karta
// phases by severity.
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

// Matches the dot color Headlamp's Pod list uses next to each container's
// status pill (mdi:circle in a plain CSS color, not a MUI palette token).
const PHASE_DOT_COLOR: Record<string, string> = {
  Failed: 'red',
  Degraded: 'orange',
  Suspending: 'orange',
  Resuming: 'blue',
  Suspended: 'grey',
  Initializing: 'grey',
  Running: 'green',
  Completed: 'blue',
  Undefined: 'grey',
};

export interface StatusPhaseChipsProps {
  phases: string[];
}

// Renders a Karta workload's matched status phases in the same pill + dot
// style as Headlamp's built-in Pod/Deployment status column, worst-severity
// first — a workload can match multiple phases simultaneously (e.g.
// ["Running", "Degraded"]).
export function StatusPhaseChips({ phases }: StatusPhaseChipsProps) {
  const sorted = [...phases].sort((a, b) => (PHASE_SEVERITY[a] ?? 99) - (PHASE_SEVERITY[b] ?? 99));
  return (
    <Stack direction="row" spacing={1} flexWrap="wrap" alignItems="center">
      {sorted.map(phase => {
        const status = PHASE_STATUS[phase] ?? '';
        return (
          <Box key={phase} display="flex" alignItems="center" gap={0.5}>
            <StatusLabel status={status}>
              {(status === 'warning' || status === 'error') && (
                <Icon aria-label="hidden" icon="mdi:alert-outline" width="1.2rem" height="1.2rem" />
              )}
              {phase}
            </StatusLabel>
            <Icon icon="mdi:circle" style={{ color: PHASE_DOT_COLOR[phase] ?? 'grey' }} width="1rem" height="1rem" />
          </Box>
        );
      })}
    </Stack>
  );
}
