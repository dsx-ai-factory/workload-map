// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Icon } from '@iconify/react';
import { StatusLabel } from '@kinvolk/headlamp-plugin/lib/CommonComponents';
import Stack from '@mui/material/Stack';
import type { StatusItem } from '../../../lib/karta/statusItem';
import { InfoTooltip } from './InfoTooltip';

export interface StatusCellProps {
  items: StatusItem[];
}

// A pill per item in the given order, each explained by its tooltip when it
// has one.
export function StatusCell({ items }: StatusCellProps) {
  return (
    <Stack direction="row" spacing={1} flexWrap="wrap" alignItems="center">
      {items.map(({ label, status, tooltip }) => {
        const pill = (
          <StatusLabel key={label} status={status}>
            {(status === 'warning' || status === 'error') && (
              <Icon aria-hidden icon="mdi:alert-outline" width="1.2rem" height="1.2rem" />
            )}
            {label}
          </StatusLabel>
        );
        return tooltip ? (
          <InfoTooltip key={label} label={label} tooltip={tooltip}>
            {pill}
          </InfoTooltip>
        ) : (
          pill
        );
      })}
    </Stack>
  );
}
