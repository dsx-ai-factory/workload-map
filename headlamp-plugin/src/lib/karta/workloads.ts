// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { K8s } from '@kinvolk/headlamp-plugin/lib';
import type { GroupVersionKind, Workload } from './karta.types';

// The list class for one workload kind. The plural and the scope come from
// discovery, because neither can be derived from the kind name or the spec.
export function makeWorkloadClass(kind: GroupVersionKind, plural: string, namespaced: boolean) {
  return K8s.crd.makeCustomResourceClass({
    apiInfo: [{ group: kind.group, version: kind.version }],
    kind: kind.kind,
    pluralName: plural,
    singularName: kind.kind.toLowerCase(),
    isNamespaced: namespaced,
  });
}

// A controller owns these, so the owner is the workload: a Deployment and its
// pods are one thing described at two levels, and listing both shows it twice.
// The Workloads table and the Instances column share this rule so they agree.
export function isRootWorkload(workload: Workload): boolean {
  return !(workload.metadata.ownerReferences ?? []).some(reference => reference.controller);
}
