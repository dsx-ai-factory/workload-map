// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import { useMemo } from 'react';

// useSelectedClusters returns an empty list when nothing is explicitly
// selected, which is the ordinary single-cluster case, so fall back to the
// current one.
export function useClusters(): string[] {
  const selectedClusters = K8s.useSelectedClusters();
  const currentCluster = K8s.useCluster();
  // The array is a new object on every render, so the memo keys off its
  // content. A newline cannot be part of a cluster name, unlike a comma.
  const selectedKey = selectedClusters.join('\n');
  return useMemo(() => {
    const selected = selectedKey ? selectedKey.split('\n') : [];
    return selected.length > 0 ? selected : currentCluster ? [currentCluster] : [];
  }, [selectedKey, currentCluster]);
}
