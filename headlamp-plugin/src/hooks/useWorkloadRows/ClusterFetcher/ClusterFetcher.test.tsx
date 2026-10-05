// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { useKartaDefinitions, useServedKinds, kindFetcherProps, getAllowedNamespaces } = vi.hoisted(
  () => ({
    useKartaDefinitions: vi.fn(),
    useServedKinds: vi.fn(),
    kindFetcherProps: [] as any[],
    getAllowedNamespaces: vi.fn(() => [] as string[]),
  })
);

vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  K8s: { cluster: { getAllowedNamespaces } },
}));
vi.mock('../../useKartaDefinitions/useKartaDefinitions', () => ({ useKartaDefinitions }));
vi.mock('../../useServedKinds/useServedKinds', () => ({
  useServedKinds,
  servedKindKey: (group: string, version: string, kind: string) =>
    `${group ? `${group}/${version}` : version}/${kind}`,
}));
vi.mock('../KindFetcher/KindFetcher', () => ({
  KindFetcher: (props: any) => {
    kindFetcherProps.push(props);
    return null;
  },
}));

import type { Definition } from '../../../lib/karta/definitions';
import type { ServedKind } from '../../useServedKinds/useServedKinds';
import { ClusterFetcher, ClusterState } from './ClusterFetcher';

function definition(name: string, group: string, version: string): Definition {
  return {
    karta: {
      apiVersion: 'run.ai/v1alpha1',
      kind: 'Karta',
      metadata: { name },
      spec: { structureDefinition: { rootComponent: { kind: { group, version, kind: name } } } },
    },
    origin: 'catalog',
  };
}

function givenDefinitions(definitions: Definition[], error: Error | null = null) {
  useKartaDefinitions.mockReturnValue({
    definitions,
    loading: false,
    error,
    installed: true,
    crdMissing: false,
  });
}

function givenDiscovery(
  served: Map<string, ServedKind> | null,
  extra: { failedGroupVersions?: Set<string>; error?: Error | null } = {}
) {
  useServedKinds.mockReturnValue({
    served,
    failedGroupVersions: extra.failedGroupVersions ?? new Set<string>(),
    loading: false,
    error: extra.error ?? null,
  });
}

const deploymentServed = new Map([
  ['apps/v1/Deployment', { plural: 'deployments', namespaced: true }],
]);

function renderCluster(cluster = 'cluster-a', namespaces?: string[]) {
  const states: ClusterState[] = [];
  render(
    <ClusterFetcher
      cluster={cluster}
      namespaces={namespaces}
      onRows={vi.fn()}
      onError={vi.fn()}
      onState={(_c, state) => states.push(state)}
    />
  );
  return states;
}

beforeEach(() => {
  kindFetcherProps.length = 0;
  useKartaDefinitions.mockReset();
  useServedKinds.mockReset();
  getAllowedNamespaces.mockReset();
  getAllowedNamespaces.mockReturnValue([]);
});

describe('ClusterFetcher', () => {
  it('mounts a fetcher only for the kinds the cluster serves', async () => {
    givenDefinitions([
      definition('Deployment', 'apps', 'v1'),
      definition('MPIJob', 'kubeflow.org', 'v1'),
    ]);
    givenDiscovery(deploymentServed);

    renderCluster();

    await waitFor(() => expect(kindFetcherProps).toHaveLength(1));
    expect(kindFetcherProps[0].plural).toBe('deployments');
    // Its own cluster, so another cluster's workloads are not read through
    // this cluster's definitions.
    expect(kindFetcherProps[0].cluster).toBe('cluster-a');
  });

  it('hands the chosen namespaces to each kind it fetches', async () => {
    givenDefinitions([definition('Deployment', 'apps', 'v1')]);
    givenDiscovery(deploymentServed);

    renderCluster('cluster-a', ['default']);

    await waitFor(() => expect(kindFetcherProps).toHaveLength(1));
    expect(kindFetcherProps[0].namespaces).toEqual(['default']);
  });

  // useList turns an empty namespace list into a request with no namespace,
  // which lists the whole cluster. Selecting one this cluster disallows must
  // not widen the read.
  it('fetches nothing when the selection and the allowed namespaces do not overlap', async () => {
    getAllowedNamespaces.mockReturnValue(['team-b']);
    givenDefinitions([definition('Deployment', 'apps', 'v1')]);
    givenDiscovery(deploymentServed);

    const states = renderCluster('cluster-a', ['team-a']);

    await waitFor(() => expect(states.length).toBeGreaterThan(0));
    expect(kindFetcherProps).toHaveLength(0);
    expect(states.at(-1)?.expectedKinds).toEqual([]);
  });

  it('narrows the selection to the namespaces the cluster allows', async () => {
    getAllowedNamespaces.mockReturnValue(['team-a', 'team-b']);
    givenDefinitions([definition('Deployment', 'apps', 'v1')]);
    givenDiscovery(deploymentServed);

    renderCluster('cluster-a', ['team-a', 'team-z']);

    await waitFor(() => expect(kindFetcherProps).toHaveLength(1));
    expect(kindFetcherProps[0].namespaces).toEqual(['team-a']);
  });

  it('passes its own cluster to the definitions and discovery hooks', () => {
    givenDefinitions([]);
    givenDiscovery(new Map());

    renderCluster('cluster-b');

    expect(useKartaDefinitions).toHaveBeenCalledWith('cluster-b');
    expect(useServedKinds).toHaveBeenCalledWith('cluster-b', expect.any(Array));
  });

  it('reports the kinds it expects to report, so a caller knows what to wait for', async () => {
    givenDefinitions([definition('Deployment', 'apps', 'v1')]);
    givenDiscovery(deploymentServed);

    const states = renderCluster();

    await waitFor(() => expect(states.length).toBeGreaterThan(0));
    expect(states.at(-1)?.expectedKinds).toEqual(['Deployment']);
    expect(states.at(-1)?.loading).toBe(false);
  });

  it('reports a kind whose group never answered rather than omitting it', async () => {
    givenDefinitions([definition('RayJob', 'ray.io', 'v1')]);
    givenDiscovery(new Map(), { failedGroupVersions: new Set(['ray.io/v1']) });

    const states = renderCluster();

    await waitFor(() => expect(states.length).toBeGreaterThan(0));
    expect(states.at(-1)?.discoveryFailures.RayJob?.message).toBe('discovery failed for ray.io/v1');
    expect(kindFetcherProps).toHaveLength(0);
  });

  // Listing catalog kinds needs no permission on kartas.run.ai, so a user who
  // cannot read the CRs still gets a full table.
  it('treats a CR list failure as partial while the catalog still describes kinds', async () => {
    givenDefinitions([definition('Deployment', 'apps', 'v1')], new Error('kartas forbidden'));
    givenDiscovery(deploymentServed);

    const states = renderCluster();

    await waitFor(() => expect(states.length).toBeGreaterThan(0));
    expect(states.at(-1)?.error).toBeNull();
    expect(states.at(-1)?.warning?.message).toBe('kartas forbidden');
    expect(states.at(-1)?.expectedKinds).toEqual(['Deployment']);
  });

  it('treats a definitions failure as fatal when nothing is left to describe', async () => {
    givenDefinitions([], new Error('kartas forbidden'));
    givenDiscovery(new Map());

    const states = renderCluster();

    await waitFor(() => expect(states.length).toBeGreaterThan(0));
    expect(states.at(-1)?.error?.message).toBe('kartas forbidden');
  });

  it('reports a discovery failure as this cluster failing', async () => {
    givenDefinitions([definition('Deployment', 'apps', 'v1')]);
    givenDiscovery(null, { error: new Error('discovery unreachable') });

    const states = renderCluster();

    await waitFor(() => expect(states.length).toBeGreaterThan(0));
    expect(states.at(-1)?.error?.message).toBe('discovery unreachable');
    expect(states.at(-1)?.expectedKinds).toEqual([]);
  });
});
