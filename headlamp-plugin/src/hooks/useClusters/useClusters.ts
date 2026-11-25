// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { useMemo } from 'react';

// useSelectedClusters returns an empty list when nothing is explicitly
// selected, which is the ordinary single-cluster case, so fall back to the
// current one. Same shape as knative's useClusters.
export function useClusters(): string[] {
  const selectedClusters = K8s.useSelectedClusters();
  const currentCluster = K8s.useCluster();
  // The array is a new object on every render, so the memo keys off its content.
  const selectedKey = JSON.stringify(selectedClusters);
  return useMemo(() => {
    const selected: string[] = JSON.parse(selectedKey);
    return selected.length > 0 ? selected : currentCluster ? [currentCluster] : [];
  }, [selectedKey, currentCluster]);
}
