// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { useKartaWasm, useKartaDefinitions, useServedKinds, clusterRef } = vi.hoisted(() => ({
  useKartaWasm: vi.fn(),
  useKartaDefinitions: vi.fn(),
  useServedKinds: vi.fn(),
  clusterRef: { current: 'cluster-a' },
}));
// useCluster reaches for Headlamp's store, which these tests do not set up.
vi.mock('@kinvolk/headlamp-plugin/lib', () => ({ K8s: { useCluster: () => clusterRef.current } }));
vi.mock('../useKartaWasm/useKartaWasm', () => ({ useKartaWasm }));
vi.mock('../useKartaDefinitions/useKartaDefinitions', () => ({ useKartaDefinitions }));
vi.mock('../useServedKinds/useServedKinds', () => ({
  useServedKinds,
  servedKindKey: (group: string, version: string, kind: string) =>
    `${group ? `${group}/${version}` : version}/${kind}`,
}));

// Serves whatever the test's definitions ask for, so the tests that are not
// about discovery keep exercising every definition they declare.
function servesEverything() {
  useServedKinds.mockImplementation(
    (_cluster: string, kinds: ({ group: string; version: string; kind: string } | undefined)[]) => ({
    served: new Map(
      kinds
        .filter(k => !!k)
        .map(k => [
          `${k.group ? `${k.group}/${k.version}` : k.version}/${k.kind}`,
          { plural: `${k.kind.toLowerCase()}s`, namespaced: true },
        ])
    ),
      failedGroupVersions: new Set<string>(),
      loading: false,
      error: null,
    })
  );
}

beforeEach(() => {
  clusterRef.current = 'cluster-a';
  servesEverything();
});

vi.mock('./KindFetcher/KindFetcher', () => ({
  KindFetcher: ({ definition, cluster, onRows, onError }: any) => {
    useEffect(() => {
      const name = definition.karta.metadata.name;
      // cluster-b stands for a list that has not resolved its first page.
      if (cluster === 'cluster-b') {
        return;
      }
      if (name === 'broken') {
        onError(name, new Error(`${name} failed to list`));
      } else if (name !== 'pending') {
        onRows(name, [{ id: name, name }]);
      }
      // 'pending' never calls onRows/onError, simulating a kind whose
      // useList() hasn't resolved its first page yet.
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);
    return null;
  },
}));

import type { Definition } from '../../lib/karta/definitions';
import type { UseWorkloadRowsResult } from './useWorkloadRows';
import { useWorkloadRows } from './useWorkloadRows';

function definition(name: string): Definition {
  return definitionForKind(name, 'example.com', 'v1');
}

function definitionForKind(name: string, group: string, version: string): Definition {
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

function Harness({ onResult }: { onResult: (result: UseWorkloadRowsResult) => void }) {
  const result = useWorkloadRows();
  onResult(result);
  return <>{result.fetchers}</>;
}

function renderHarness() {
  let latest: UseWorkloadRowsResult | undefined;
  const { rerender } = render(<Harness onResult={r => (latest = r)} />);
  return {
    get current() {
      return latest!;
    },
    rerender: () => rerender(<Harness onResult={r => (latest = r)} />),
  };
}

describe('useWorkloadRows', () => {
  it('reports loading while the engine or definitions are still loading', () => {
    useKartaWasm.mockReturnValue({ karta: null, loading: true, error: null });
    useKartaDefinitions.mockReturnValue({ definitions: [], loading: false, error: null, installed: true, crdMissing: false });

    const harness = renderHarness();

    expect(harness.current.loading).toBe(true);
    expect(harness.current.rows).toBeNull();
  });

  it('does not fetch a kind the cluster does not serve', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    // kubeflow.org/v1 is served, but MPIJob is not one of its resources.
    useServedKinds.mockReturnValue({
      served: new Map([['apps/v1/Deployment', { plural: 'deployments', namespaced: true }]]),
      failedGroupVersions: new Set<string>(),
      loading: false,
      error: null,
    });
    useKartaDefinitions.mockReturnValue({
      definitions: [
        definitionForKind('Deployment', 'apps', 'v1'),
        definitionForKind('MPIJob', 'kubeflow.org', 'v1'),
      ],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();

    // MPIJob never gets a fetcher, so loading clears on Deployment alone
    // rather than waiting for a list request that would 404 and be retried.
    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.rows).toEqual([{ id: 'Deployment', name: 'Deployment' }]);
  });

  it('aggregates rows from each definition once fetchers resolve', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('deployment'), definition('pytorchjob')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.rows).toHaveLength(2));
    expect(harness.current.rows).toEqual(
      expect.arrayContaining([
        { id: 'deployment', name: 'deployment' },
        { id: 'pytorchjob', name: 'pytorchjob' },
      ])
    );
    expect(harness.current.loading).toBe(false);
    expect(harness.current.error).toBeNull();
  });

  it('surfaces the engine error and never blocks on a single kind failing', async () => {
    useKartaWasm.mockReturnValue({ karta: null, loading: false, error: new Error('wasm load failed') });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('broken')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.error?.message).toBe('wasm load failed'));
  });

  it('stays loading until every definition has reported rows or an error', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('deployment'), definition('pending')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();

    // 'deployment' resolves immediately (its effect already ran by the time
    // render() returns) but 'pending' never does — the table must not flip
    // to its empty/loaded state on 'deployment' alone.
    expect(harness.current.loading).toBe(true);
    expect(harness.current.rows).toBeNull();
  });

  // A kind failing to list is partial: the kinds that loaded are still worth
  // showing, so it is reported beside them rather than as a fatal error.
  it('reports a per-kind list error without making it fatal', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('broken'), definition('deployment')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();

    await waitFor(() =>
      expect(harness.current.errorsByKind.broken?.message).toBe('broken failed to list')
    );
    expect(harness.current.error).toBeNull();
    expect(harness.current.rows).toEqual([{ id: 'deployment', name: 'deployment' }]);
  });

  // served null with loading settled is what a failed discovery looks like.
  // Without the error, that is indistinguishable from a cluster serving none
  // of the catalog, and the table renders empty as though nothing is wrong.
  it('surfaces a discovery failure rather than rendering an empty table', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('deployment')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });
    useServedKinds.mockReturnValue({
      served: null,
      failedGroupVersions: new Set<string>(),
      loading: false,
      error: new Error('discovery unreachable'),
    });

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.error?.message).toBe('discovery unreachable'));
  });

  // A group that did not answer leaves its kinds out of `served`, which is
  // indistinguishable from the cluster not having them unless it is reported.
  it('reports a kind whose group discovery failed rather than omitting it silently', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useServedKinds.mockReturnValue({
      served: new Map([['apps/v1/Deployment', { plural: 'deployments', namespaced: true }]]),
      failedGroupVersions: new Set(['ray.io/v1']),
      loading: false,
      error: null,
    });
    useKartaDefinitions.mockReturnValue({
      definitions: [
        definitionForKind('Deployment', 'apps', 'v1'),
        definitionForKind('RayJob', 'ray.io', 'v1'),
      ],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    // Deployment still loads, and RayJob's absence is explained rather than
    // silent.
    expect(harness.current.rows).toEqual([{ id: 'Deployment', name: 'Deployment' }]);
    expect(harness.current.errorsByKind.RayJob?.message).toBe('discovery failed for ray.io/v1');
    expect(harness.current.error).toBeNull();
  });

  // Definition names repeat across clusters, so rows held under a name must
  // not be read as the next cluster's.
  it('drops the previous cluster rows when the cluster changes', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('deployment')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();
    await waitFor(() => expect(harness.current.rows).toEqual([{ id: 'deployment', name: 'deployment' }]));

    clusterRef.current = 'cluster-b';
    harness.rerender();

    // cluster-b's fetcher has not reported, so there is nothing to show yet.
    await waitFor(() => expect(harness.current.loading).toBe(true));
    expect(harness.current.rows).toBeNull();
  });

  it('stops reporting an error for a kind the cluster no longer serves', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('broken'), definition('deployment')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();
    await waitFor(() =>
      expect(harness.current.errorsByKind.broken?.message).toBe('broken failed to list')
    );

    // Only 'deployment' is served now, so 'broken' has no fetcher to own its
    // error.
    useServedKinds.mockReturnValue({
      served: new Map([['example.com/v1/deployment', { plural: 'deployments', namespaced: true }]]),
      failedGroupVersions: new Set<string>(),
      loading: false,
      error: null,
    });
    harness.rerender();

    await waitFor(() => expect(harness.current.errorsByKind).toEqual({}));
    expect(harness.current.error).toBeNull();
  });
});
