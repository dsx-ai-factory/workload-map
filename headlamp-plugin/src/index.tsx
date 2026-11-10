// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import {
  registerPluginSettings,
  registerRoute,
  registerSidebarEntry,
} from '@kinvolk/headlamp-plugin/lib';
import { Settings } from './components/settings/Settings';
import { WorkloadsPage } from './pages/workloadsPage/WorkloadsPage';

registerPluginSettings('karta', Settings, true);

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
