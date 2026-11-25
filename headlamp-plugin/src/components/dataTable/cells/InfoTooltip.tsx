// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import Tooltip from '@mui/material/Tooltip';
import type { CSSProperties, ReactNode } from 'react';

export interface InfoTooltipProps {
  label: string;
  tooltip: string;
  style?: CSSProperties;
  children: ReactNode;
}

// Content explained by a tooltip that the keyboard can reach. A tooltip on an
// element that cannot take focus never opens from the keyboard, and the lint
// rules accept a tab stop only on an interactive role, so the button role is
// the one that works here although the element does nothing when activated.
export function InfoTooltip({ label, tooltip, style, children }: InfoTooltipProps) {
  return (
    <Tooltip title={tooltip}>
      <span role="button" tabIndex={0} aria-label={`${label}: ${tooltip}`} style={style}>
        {children}
      </span>
    </Tooltip>
  );
}
