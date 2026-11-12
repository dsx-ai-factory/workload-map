// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Settings } from './Settings';

describe('Settings', () => {
  it('starts disabled and stages an opt-in for Headlamp to save', () => {
    const onDataChange = vi.fn();
    render(<Settings data={undefined} onDataChange={onDataChange} />);

    const toggle = screen.getByRole('checkbox', { name: 'Enable experimental definitions' });
    expect((toggle as HTMLInputElement).checked).toBe(false);
    expect(onDataChange).not.toHaveBeenCalled();

    fireEvent.click(toggle);
    expect(onDataChange).toHaveBeenCalledWith({ enableExperimental: true });
  });

  it('reads the saved setting and preserves other settings when disabling it', () => {
    const onDataChange = vi.fn();
    render(
      <Settings
        data={{ enableExperimental: true, anotherSetting: 'preserved' }}
        onDataChange={onDataChange}
      />
    );

    const toggle = screen.getByRole('checkbox', { name: 'Enable experimental definitions' });
    expect((toggle as HTMLInputElement).checked).toBe(true);
    fireEvent.click(toggle);
    expect(onDataChange).toHaveBeenCalledWith({
      enableExperimental: false,
      anotherSetting: 'preserved',
    });
  });
});
