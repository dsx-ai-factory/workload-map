// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it, vi } from 'vitest';

vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  K8s: { crd: { makeCustomResourceClass: vi.fn(() => ({ useList: vi.fn() })) } },
}));

import { mergeDefinitions, rootGVKKey } from './definitions';
import type { Karta } from './karta.types';

function karta(name: string, kind?: { group: string; version: string; kind: string }): Karta {
  return {
    apiVersion: 'run.ai/v1alpha1',
    kind: 'Karta',
    metadata: { name },
    spec: { structureDefinition: { rootComponent: { kind } } },
  };
}

const deploymentGVK = { group: 'apps', version: 'v1', kind: 'Deployment' };
const jobGVK = { group: 'batch', version: 'v1', kind: 'Job' };

describe('rootGVKKey', () => {
  it('formats the key from the root component kind', () => {
    expect(rootGVKKey(karta('deployment', deploymentGVK))).toBe('apps/v1, Kind=Deployment');
  });

  it('has no key when the root component has no kind', () => {
    expect(rootGVKKey(karta('no-kind'))).toBeNull();
  });

  // The CRD does not require spec, so a Karta applied while the validating
  // webhook is down reaches the plugin without one.
  it('has no key, rather than throwing, when the Karta has no spec at all', () => {
    const specless = { apiVersion: 'run.ai/v1alpha1', kind: 'Karta', metadata: { name: 'specless' } };

    expect(rootGVKKey(specless as Karta)).toBeNull();
  });

  it('has no key when the root kind is missing a version', () => {
    expect(rootGVKKey(karta('no-version', { group: 'apps', version: '', kind: 'Deployment' }))).toBeNull();
  });

  it('keys a core kind, whose group is empty', () => {
    expect(rootGVKKey(karta('pod', { group: '', version: 'v1', kind: 'Pod' }))).toBe('/v1, Kind=Pod');
  });
});

describe('mergeDefinitions', () => {
  it('returns catalog-only entries when there are no cluster kartas', () => {
    const catalog = [karta('catalog-deployment', deploymentGVK)];

    const merged = mergeDefinitions(catalog, []);

    expect(merged).toEqual([{ karta: catalog[0], origin: 'catalog' }]);
  });

  it('lets a cluster karta override a catalog entry for the same GVK', () => {
    const catalogDeployment = karta('catalog-deployment', deploymentGVK);
    const clusterDeployment = karta('cluster-deployment', deploymentGVK);

    const merged = mergeDefinitions([catalogDeployment], [clusterDeployment]);

    expect(merged).toEqual([{ karta: clusterDeployment, origin: 'cluster' }]);
  });

  it('skips a spec-less Karta instead of failing the whole merge', () => {
    const specless = { apiVersion: 'run.ai/v1alpha1', kind: 'Karta', metadata: { name: 'specless' } };
    const clusterDeployment = karta('cluster-deployment', deploymentGVK);

    const merged = mergeDefinitions([], [specless as Karta, clusterDeployment]);

    // One incomplete object must not take the valid definitions down with it.
    expect(merged).toEqual([{ karta: clusterDeployment, origin: 'cluster' }]);
  });

  it('leaves out definitions with no root GVK rather than letting them collide', () => {
    const firstRootless = karta('rootless-one');
    const secondRootless = karta('rootless-two');
    const clusterDeployment = karta('cluster-deployment', deploymentGVK);

    const merged = mergeDefinitions([firstRootless, secondRootless], [clusterDeployment]);

    expect(merged).toEqual([{ karta: clusterDeployment, origin: 'cluster' }]);
  });

  it('keeps non-colliding catalog and cluster entries side by side', () => {
    const catalogJob = karta('catalog-job', jobGVK);
    const clusterDeployment = karta('cluster-deployment', deploymentGVK);

    const merged = mergeDefinitions([catalogJob], [clusterDeployment]);

    expect(merged).toEqual(
      expect.arrayContaining([
        { karta: catalogJob, origin: 'catalog' },
        { karta: clusterDeployment, origin: 'cluster' },
      ])
    );
    expect(merged).toHaveLength(2);
  });
});
