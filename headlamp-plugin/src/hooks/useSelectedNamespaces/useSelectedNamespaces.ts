// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { useMemo } from 'react';
import { useSelector } from 'react-redux';

interface FilterState {
  namespaces: Set<string>;
}

// Read from the host's store because the plugin API does not re-export
// useNamespaces. undefined when nothing is chosen: useList reads that as every
// namespace, where [] would ask for none.
export function useSelectedNamespaces(): string[] | undefined {
  const namespaces = useSelector(({ filter }: { filter: FilterState }) => filter.namespaces);
  return useMemo(
    () => (namespaces.size === 0 ? undefined : [...namespaces]),
    [namespaces]
  );
}
