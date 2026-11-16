// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import type { GroupVersionKind } from './karta.types';

// gvkKey mirrors pkg/catalog.RootKey's key shape, so a cluster CR and a
// catalog entry describing the same workload kind collide on the same key. It
// identifies a definition: names do not, since a cluster Karta can reuse the
// name the catalog gives a different kind.
export function gvkKey(kind: GroupVersionKind): string {
  return `${kind.group}/${kind.version}, Kind=${kind.kind}`;
}
