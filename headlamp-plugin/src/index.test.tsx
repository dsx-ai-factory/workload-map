// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it, vi } from 'vitest';

const { registerRoute, registerSidebarEntry, getKartaWasm } = vi.hoisted(() => ({
  registerRoute: vi.fn(),
  registerSidebarEntry: vi.fn(),
  getKartaWasm: vi.fn(() => Promise.resolve({})),
}));

vi.mock('@kinvolk/headlamp-plugin/lib', () => ({ registerRoute, registerSidebarEntry }));
vi.mock('./lib/karta', () => ({ getKartaWasm }));
vi.mock('./pages/workloadsPage/WorkloadsPage', () => ({ WorkloadsPage: () => null }));

// Importing registers the plugin, which is the behaviour under test.
import './index';

describe('plugin registration', () => {
  // The engine is ~19MB, and every Headlamp user would pay for it at startup
  // whether or not they open Karta. The page loads it on mount instead.
  it('does not load the engine when the plugin is registered', () => {
    expect(getKartaWasm).not.toHaveBeenCalled();
  });

  it('registers the workloads route and its sidebar entries', () => {
    expect(registerRoute).toHaveBeenCalledWith(
      expect.objectContaining({ path: '/karta/workloads', exact: true })
    );
    expect(registerSidebarEntry).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'karta-workloads', url: '/karta/workloads' })
    );
  });
});
