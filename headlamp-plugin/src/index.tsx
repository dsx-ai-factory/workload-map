// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { registerRoute, registerSidebarEntry } from '@kinvolk/headlamp-plugin/lib';
import { getKartaWasm } from './lib/karta';
import { WorkloadsPage } from './pages';

// Kick off the WASM module load (fetch + instantiate karta.wasm, ~19MB) as
// soon as the plugin's module loads — i.e. when Headlamp itself starts —
// instead of waiting for the user to navigate to the Workloads page.
// getKartaWasm() caches its promise, so useKartaWasm() (called from
// WorkloadsPage) reuses this same in-flight/resolved load rather than
// starting a second one. Errors are swallowed here only to avoid an
// unhandled-rejection log; getKartaWasm() itself resets its cache on
// failure, so useKartaWasm() still retries and surfaces the real error
// when the page mounts.
getKartaWasm().catch(() => {});

registerSidebarEntry({
  parent: null,
  name: 'karta',
  label: 'Karta',
  icon: 'mdi:graph-outline',
});

registerSidebarEntry({
  parent: 'karta',
  name: 'karta-workloads',
  label: 'Workloads',
  url: '/karta/workloads',
});

registerRoute({
  path: '/karta/workloads',
  sidebar: 'karta-workloads',
  name: 'karta-workloads',
  exact: true,
  component: WorkloadsPage,
});
