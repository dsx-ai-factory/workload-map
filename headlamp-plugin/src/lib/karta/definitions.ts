// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import type { Karta } from './karta.types';

export type DefinitionOrigin = 'catalog' | 'cluster';

export interface Definition {
  karta: Karta;
  origin: DefinitionOrigin;
}

// KartaCR is the kartas.run.ai CRD, cluster-scoped per
// charts/karta/crds/run.ai_kartas.yaml.
export const KartaCR = K8s.crd.makeCustomResourceClass({
  apiInfo: [{ group: 'run.ai', version: 'v1alpha1' }],
  kind: 'Karta',
  pluralName: 'kartas',
  singularName: 'karta',
  isNamespaced: false,
});

// rootGVKKey mirrors pkg/catalog.RootKey's key shape, so a cluster CR and a
// catalog entry describing the same workload kind collide on the same key.
// Group is empty for core kinds such as Pod, but pkg/catalog requires a version
// and a kind: without them the definition names no workload, so it has no key.
export function rootGVKKey(karta: Karta): string | null {
  const kind = karta.spec.structureDefinition.rootComponent.kind;
  if (!kind?.version || !kind.kind) {
    return null;
  }
  return `${kind.group}/${kind.version}, Kind=${kind.kind}`;
}

// mergeDefinitions indexes catalogKartas by their root GVK, then lets
// clusterKartas overwrite any catalog entry claiming the same GVK — cluster
// definitions take priority, catalog-only kinds are still surfaced. A
// definition with no root GVK matches no workload, so it is left out rather
// than merged with every other one under a shared key.
export function mergeDefinitions(catalogKartas: Karta[], clusterKartas: Karta[]): Definition[] {
  const byKey = new Map<string, Definition>();

  const index = (kartas: Karta[], origin: DefinitionOrigin) => {
    for (const karta of kartas) {
      const key = rootGVKKey(karta);
      if (key !== null) {
        byKey.set(key, { karta, origin });
      }
    }
  };

  index(catalogKartas, 'catalog');
  index(clusterKartas, 'cluster');

  return Array.from(byKey.values());
}
