// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { registerRoute, registerSidebarEntry } from '@kinvolk/headlamp-plugin/lib';
import { getKartaWasm } from './lib/karta';
import { WorkloadsPage } from './pages/workloadsPage/WorkloadsPage';

// Starts the ~19MB WASM load when Headlamp starts rather than on first visit.
// getKartaWasm caches its promise, so useKartaWasm reuses this one. The catch
// only avoids an unhandled rejection: the cache resets on failure, so the page
// still retries and surfaces the error.
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
