// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { ConfigStore } from '@kinvolk/headlamp-plugin/lib';

export interface KartaConfig {
  enableExperimental?: boolean;
}

const configStore = new ConfigStore<KartaConfig>('karta');

export const useKartaConfig = configStore.useConfig();
