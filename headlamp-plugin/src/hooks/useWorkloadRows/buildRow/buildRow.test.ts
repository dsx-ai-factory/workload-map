// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it } from 'vitest';
import type { Definition } from '../../../lib/karta/definitions';
import type { Karta, Workload } from '../../../lib/karta/karta.types';
import { buildWorkloadRow } from './buildRow';

function karta(childComponents?: { name: string }[]): Karta {
  return {
    apiVersion: 'run.ai/v1alpha1',
    kind: 'Karta',
    metadata: { name: 'pytorchjob' },
    spec: {
      structureDefinition: {
        rootComponent: { kind: { group: 'kubeflow.org', version: 'v1', kind: 'PyTorchJob' } },
        childComponents,
      },
    },
  };
}

function workload(): Workload {
  return {
    apiVersion: 'kubeflow.org/v1',
    kind: 'PyTorchJob',
    metadata: { name: 'bert-training', namespace: 'default', creationTimestamp: '2026-08-01T00:00:00Z' },
  };
}

describe('buildWorkloadRow', () => {
  it('projects a live workload + definition into a row', () => {
    const definition: Definition = { karta: karta([{ name: 'master' }, { name: 'worker' }]), origin: 'catalog' };

    const row = buildWorkloadRow(definition, workload(), 'local');

    expect(row).toMatchObject({
      id: 'local/default/PyTorchJob/bert-training',
      name: 'bert-training',
      namespace: 'default',
      cluster: 'local',
      kind: 'PyTorchJob',
      apiGroup: 'kubeflow.org',
      detailPath: '/karta/workloads/kubeflow.org/v1/PyTorchJob/default/bert-training',
      phases: [],
      componentsCount: 3,
      instancesCount: null,
      podsReady: null,
      podsDesired: null,
      gpusRequested: null,
      cpuRequestMillis: null,
      memoryRequestBytes: null,
      rawPhase: null,
    });
  });

  it('counts only the root component when there are no child components', () => {
    const definition: Definition = { karta: karta(undefined), origin: 'catalog' };

    const row = buildWorkloadRow(definition, workload(), 'local');

    expect(row.componentsCount).toBe(1);
  });
});
