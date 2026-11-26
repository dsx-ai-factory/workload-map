// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

  const badge = () => (
    <InfoTooltip label="Unavailable" tooltip="engine down">
      badge
    </InfoTooltip>
  );
  const tooltipClosed = () => waitFor(() => expect(screen.queryByRole('tooltip')).toBeNull());

  it('opens on focus and closes on Escape, with focus staying on the trigger', async () => {
    const user = userEvent.setup();
    render(badge());

    await user.tab();
    expect((await screen.findByRole('tooltip')).textContent).toBe('engine down');

    await user.keyboard('{Escape}');
    await tooltipClosed();
    expect(document.activeElement).toBe(screen.getByRole('button'));
  });

  it.each([
    ['Enter', '{Enter}'],
    ['Space', ' '],
  ])('opens the tooltip again with %s after Escape closed it', async (_name, key) => {
    const user = userEvent.setup();
    render(badge());

    await user.tab();
    await screen.findByRole('tooltip');
    await user.keyboard('{Escape}');
    await tooltipClosed();

    await user.keyboard(key);
    expect((await screen.findByRole('tooltip')).textContent).toBe('engine down');
    expect(document.activeElement).toBe(screen.getByRole('button'));
  });

  it('opens the tooltip again for the pointer after Escape closed it', async () => {
    const user = userEvent.setup();
    render(badge());

    await user.tab();
    await screen.findByRole('tooltip');
    await user.keyboard('{Escape}');
    await tooltipClosed();

    await user.click(screen.getByRole('button'));
    expect(await screen.findByRole('tooltip')).toBeTruthy();
  });

  it('does not scroll the page when Space is pressed', async () => {
    const user = userEvent.setup();
    render(badge());

    await user.tab();
    const press = new KeyboardEvent('keydown', { key: ' ', bubbles: true, cancelable: true });
    screen.getByRole('button').dispatchEvent(press);
    expect(press.defaultPrevented).toBe(true);
  });
});
