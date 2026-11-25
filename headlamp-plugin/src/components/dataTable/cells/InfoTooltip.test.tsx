// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { InfoTooltip } from './InfoTooltip';

describe('InfoTooltip', () => {
  it('puts its content in a labelled tab stop that explains itself', () => {
    render(
      <InfoTooltip label="Error" tooltip="boom">
        content
      </InfoTooltip>
    );
    const stop = screen.getByRole('button', { name: 'Error: boom' });
    expect(stop.getAttribute('tabindex')).toBe('0');
    expect(stop.textContent).toBe('content');
  });

  it('passes the style on to the tab stop', () => {
    render(
      <InfoTooltip label="a" tooltip="b" style={{ opacity: 0.5 }}>
        x
      </InfoTooltip>
    );
    expect(screen.getByRole('button').style.opacity).toBe('0.5');
  });
});
