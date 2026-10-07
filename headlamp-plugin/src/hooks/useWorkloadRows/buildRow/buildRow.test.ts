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
      id: 'local/kubeflow.org/v1/default/PyTorchJob/bert-training',
      name: 'bert-training',
      namespace: 'default',
      cluster: 'local',
      kind: 'PyTorchJob',
      apiGroup: 'kubeflow.org',
      phases: [],
      componentsCount: 3,
    });
  });

  // The catalog describes DynamoGraphDeployment at both v1alpha1 and v1beta1,
  // and a cluster serving both lists the same object under each, so the id has
  // to separate them or the two rows collide.
  it('gives the same object distinct ids when listed under two api versions', () => {
    const dynamo = (version: string): Karta => ({
      apiVersion: 'run.ai/v1alpha1',
      kind: 'Karta',
      metadata: { name: `dynamo-${version}` },
      spec: {
        structureDefinition: {
          rootComponent: {
            kind: { group: 'nvidia.com', version, kind: 'DynamoGraphDeployment' },
          },
        },
      },
    });
    const instance = (version: string): Workload => ({
      apiVersion: `nvidia.com/${version}`,
      kind: 'DynamoGraphDeployment',
      metadata: { name: 'llama', namespace: 'default' },
    });

    const alpha = buildWorkloadRow({ karta: dynamo('v1alpha1'), origin: 'catalog' }, instance('v1alpha1'), 'local');
    const beta = buildWorkloadRow({ karta: dynamo('v1beta1'), origin: 'catalog' }, instance('v1beta1'), 'local');

    expect(alpha.id).not.toBe(beta.id);
  });

  it('counts only the root component when there are no child components', () => {
    const definition: Definition = { karta: karta(undefined), origin: 'catalog' };

    const row = buildWorkloadRow(definition, workload(), 'local');

    expect(row.componentsCount).toBe(1);
  });
});
