// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import Tooltip from '@mui/material/Tooltip';
import { type CSSProperties, type MouseEvent, type ReactNode, useRef, useState } from 'react';

export interface InfoTooltipProps {
  label: string;
  tooltip: string;
  style?: CSSProperties;
  children: ReactNode;
}

// Content explained by a tooltip that the keyboard can reach. A tooltip on an
// element that cannot take focus never opens from the keyboard, and the lint
// rules accept a tab stop only on an interactive role, so the button role is
// the one that works here. Escape closes the tooltip while focus stays on the
// trigger, so activating the trigger, by key or by click, opens it again, as a
// button should. A click matters when the pointer is still over the trigger and
// no new hover arrives. The name is the label alone, and the tooltip is its
// description: it opens on focus, so a long explanation is read once as a
// description and not repeated as part of the name. Once Escape has dismissed
// it, the pointer moving between the parts of the trigger, such as its text and
// its icon, does not open it again: that takes a fresh hover, a click or a key.
export function InfoTooltip({ label, tooltip, style, children }: InfoTooltipProps) {
  const [open, setOpen] = useState(false);
  const dismissedByKey = useRef(false);
  const trigger = useRef<HTMLSpanElement>(null);

  const show = () => setOpen(true);
  return (
    <Tooltip
      title={tooltip}
      describeChild
      open={open}
      onOpen={event => {
        // Hover events bubble up from the parts inside the trigger, and say
        // where the pointer came from.
        const cameFrom = (event as MouseEvent).relatedTarget as Node | null;
        const withinTrigger = !!cameFrom && !!trigger.current?.contains(cameFrom);
        if (event.type === 'mouseover' && dismissedByKey.current && withinTrigger) {
          return;
        }
        show();
      }}
      onClose={event => {
        dismissedByKey.current = event.type === 'keydown';
        setOpen(false);
      }}
    >
      <span
        ref={trigger}
        role="button"
        tabIndex={0}
        aria-label={label}
        style={style}
        onClick={show}
        onKeyDown={event => {
          if (event.key === 'Enter' || event.key === ' ') {
            // Space would otherwise scroll the page.
            event.preventDefault();
            show();
          }
        }}
      >
        {children}
      </span>
    </Tooltip>
  );
}
