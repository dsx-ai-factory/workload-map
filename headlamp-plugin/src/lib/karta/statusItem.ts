// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// StatusLabel understands only these four buckets, the same ones Headlamp's
// own workload views use. It lives in lib, not with the cell, because lib
// code produces these items and lib must not depend on components.
export interface StatusItem {
  label: string;
  status: 'success' | 'warning' | 'error' | '';
  tooltip?: string;
}
