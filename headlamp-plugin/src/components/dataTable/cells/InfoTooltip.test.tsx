// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
    const stop = screen.getByRole('button', { name: 'Error' });
    expect(stop.getAttribute('tabindex')).toBe('0');
    expect(stop.textContent).toBe('content');
  });

  // A long evaluation error as part of the name would be read in full on
  // every focus. As the description it comes with the tooltip that focus opens.
  it('names the trigger by its label and describes it with the open tooltip', async () => {
    const user = userEvent.setup();
    render(
      <InfoTooltip label="Unavailable" tooltip="engine down: a long multi-line message">
        badge
      </InfoTooltip>
    );
    const trigger = screen.getByRole('button', { name: 'Unavailable' });
    expect(trigger.getAttribute('aria-label')).toBe('Unavailable');
    expect(trigger.hasAttribute('aria-describedby')).toBe(false);

    await user.tab();
    const tooltip = await screen.findByRole('tooltip');
    expect(trigger.getAttribute('aria-describedby')).toBe(tooltip.id);
    expect(tooltip.textContent).toBe('engine down: a long multi-line message');
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

  // The pointer never leaves the trigger, so a click is the only event left to
  // open the tooltip: no new hover arrives. userEvent's click moves the pointer
  // again, which would hide that, so the click is dispatched on its own.
  it('opens the tooltip again on a click while the pointer stays over the trigger', async () => {
    const user = userEvent.setup();
    render(badge());
    const trigger = screen.getByRole('button');

    await user.hover(trigger);
    await screen.findByRole('tooltip');
    await user.keyboard('{Escape}');
    await tooltipClosed();

    fireEvent.click(trigger);
    expect((await screen.findByRole('tooltip')).textContent).toBe('engine down');
  });

  // A handler that took every key would swallow Tab, and the keyboard user
  // could not leave the badge.
  it('leaves every other key alone: no tooltip, and the default is not prevented', async () => {
    const user = userEvent.setup();
    render(badge());

    await user.tab();
    await screen.findByRole('tooltip');
    await user.keyboard('{Escape}');
    await tooltipClosed();

    const trigger = screen.getByRole('button');
    for (const key of ['Tab', 'ArrowDown', 'a', 'Escape']) {
      const notPrevented = fireEvent.keyDown(trigger, { key });
      expect(notPrevented).toBe(true);
    }
    expect(screen.queryByRole('tooltip')).toBeNull();
  });

  describe('after Escape dismissed it with the pointer over the badge', () => {
    const iconBadge = () => (
      <InfoTooltip label="Unavailable" tooltip="engine down">
        <i data-testid="icon" />
        <span data-testid="text">Unavailable</span>
      </InfoTooltip>
    );
    // The tooltip opens after a short delay, so a tooltip that should stay
    // closed has to be given that long.
    const settle = () => act(() => new Promise(resolve => setTimeout(resolve, 300)));

    const dismissed = async () => {
      const user = userEvent.setup();
      render(iconBadge());
      await user.hover(screen.getByTestId('text'));
      await screen.findByRole('tooltip');
      await user.keyboard('{Escape}');
      await tooltipClosed();
      return user;
    };

    it('stays closed while the pointer moves between the text and the icon', async () => {
      await dismissed();

      fireEvent.mouseOver(screen.getByTestId('icon'), {
        relatedTarget: screen.getByTestId('text'),
      });
      await settle();
      expect(screen.queryByRole('tooltip')).toBeNull();

      fireEvent.mouseOver(screen.getByTestId('text'), {
        relatedTarget: screen.getByTestId('icon'),
      });
      await settle();
      expect(screen.queryByRole('tooltip')).toBeNull();

      // Onto the badge itself from one of its parts.
      fireEvent.mouseOver(screen.getByRole('button'), {
        relatedTarget: screen.getByTestId('icon'),
      });
      await settle();
      expect(screen.queryByRole('tooltip')).toBeNull();
    });

    it('opens again on a fresh hover from outside the badge', async () => {
      await dismissed();

      fireEvent.mouseOver(screen.getByTestId('icon'), { relatedTarget: document.body });
      expect(await screen.findByRole('tooltip')).toBeTruthy();
    });

    it('opens again on a click, Enter and Space while the pointer stays inside', async () => {
      const user = await dismissed();
      const trigger = screen.getByRole('button');

      fireEvent.click(trigger);
      await screen.findByRole('tooltip');

      for (const key of ['{Enter}', ' ']) {
        await user.keyboard('{Escape}');
        await tooltipClosed();
        fireEvent.mouseOver(screen.getByTestId('icon'), {
          relatedTarget: screen.getByTestId('text'),
        });
        await settle();
        expect(screen.queryByRole('tooltip')).toBeNull();

        trigger.focus();
        await user.keyboard(key);
        expect(await screen.findByRole('tooltip')).toBeTruthy();
      }
    });
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
