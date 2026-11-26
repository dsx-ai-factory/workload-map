// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it, vi } from 'vitest';

const { makeCustomResourceClass } = vi.hoisted(() => ({
  makeCustomResourceClass: vi.fn(() => 'a class'),
}));
vi.mock('@kinvolk/headlamp-plugin/lib', () => ({ K8s: { crd: { makeCustomResourceClass } } }));

import type { Workload } from './karta.types';
import { isRootWorkload, makeWorkloadClass } from './workloads';

const workload = (ownerReferences?: { controller?: boolean }[]): Workload => ({
  apiVersion: 'apps/v1',
  kind: 'Deployment',
  metadata: { name: 'a', ownerReferences },
});

describe('isRootWorkload', () => {
  it('keeps a workload nothing owns', () => {
    expect(isRootWorkload(workload())).toBe(true);
    expect(isRootWorkload(workload([]))).toBe(true);
  });

  it('keeps a workload whose owner is not its controller', () => {
    expect(isRootWorkload(workload([{ controller: false }, {}]))).toBe(true);
  });

  it('drops a workload a controller owns', () => {
    expect(isRootWorkload(workload([{ controller: true }]))).toBe(false);
    expect(isRootWorkload(workload([{}, { controller: true }]))).toBe(false);
  });
});

describe('makeWorkloadClass', () => {
  it('builds the class from the kind and what discovery reported', () => {
    const kind = { group: 'kubeflow.org', version: 'v2beta1', kind: 'MPIJob' };
    expect(makeWorkloadClass(kind, 'mpijobs', true)).toBe('a class');
    expect(makeCustomResourceClass).toHaveBeenCalledWith({
      apiInfo: [{ group: 'kubeflow.org', version: 'v2beta1' }],
      kind: 'MPIJob',
      pluralName: 'mpijobs',
      singularName: 'mpijob',
      isNamespaced: true,
    });
  });
});
