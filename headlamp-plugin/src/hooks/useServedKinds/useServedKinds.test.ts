// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock('@kinvolk/headlamp-plugin/lib', () => ({ ApiProxy: { request } }));

import { servedKindKey, useServedKinds } from './useServedKinds';

const apps = { group: 'apps', version: 'v1', kind: 'Deployment' };
const pod = { group: '', version: 'v1', kind: 'Pod' };
const mpiV1 = { group: 'kubeflow.org', version: 'v1', kind: 'MPIJob' };
const rayJob = { group: 'ray.io', version: 'v1', kind: 'RayJob' };

// kubeflow.org/v1 is served and holds PyTorchJob, but MPIJob lives in
// v2beta1 — the case a group-level check gets wrong.
function mockDiscovery() {
  request.mockImplementation((path: string) => {
    switch (path) {
      case '/apis':
        return Promise.resolve({
          groups: [
            { versions: [{ groupVersion: 'apps/v1' }] },
            { versions: [{ groupVersion: 'kubeflow.org/v1' }] },
          ],
        });
      case '/apis/apps/v1':
        return Promise.resolve({
          resources: [
            { name: 'deployments', kind: 'Deployment', namespaced: true },
            { name: 'deployments/status', kind: 'Deployment', namespaced: true },
          ],
        });
      case '/apis/kubeflow.org/v1':
        return Promise.resolve({
          resources: [{ name: 'pytorchjobs', kind: 'PyTorchJob', namespaced: true }],
        });
      case '/api/v1':
        return Promise.resolve({
          resources: [
            { name: 'pods', kind: 'Pod', namespaced: true },
            { name: 'namespaces', kind: 'Namespace', namespaced: false },
          ],
        });
      default:
        return Promise.reject(new Error(`unexpected request: ${path}`));
    }
  });
}

describe('useServedKinds', () => {
  beforeEach(() => {
    request.mockReset();
  });

  it('reports a kind whose group is served but whose resource is not', async () => {
    mockDiscovery();

    const { result } = renderHook(() => useServedKinds('cluster-a', [apps, mpiV1]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))).toEqual({
      plural: 'deployments',
      namespaced: true,
    });
    // The group is served, the kind is not.
    expect(result.current.served?.has(servedKindKey('kubeflow.org', 'v1', 'MPIJob'))).toBe(false);
  });

  it('never asks for the resource list of a group the cluster does not serve', async () => {
    mockDiscovery();

    const { result } = renderHook(() => useServedKinds('cluster-a', [apps, rayJob]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    // Compare paths only. Matching the whole call would never fail, since the
    // second argument is always { cluster } and never the {} being asserted.
    const paths = request.mock.calls.map(call => call[0]);
    expect(paths).not.toContain('/apis/ray.io/v1');
    expect(result.current.served?.has(servedKindKey('ray.io', 'v1', 'RayJob'))).toBe(false);
  });

  it('reads core kinds from /api/v1, which is not part of /apis', async () => {
    mockDiscovery();

    const { result } = renderHook(() => useServedKinds('cluster-a', [pod]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served?.get(servedKindKey('', 'v1', 'Pod'))).toEqual({
      plural: 'pods',
      namespaced: true,
    });
  });

  it('keeps the real plural rather than one derived from the kind name', async () => {
    request.mockImplementation((path: string) =>
      path === '/apis'
        ? Promise.resolve({ groups: [{ versions: [{ groupVersion: 'milvus.io/v1beta1' }] }] })
        : Promise.resolve({
            resources: [{ name: 'milvuses', kind: 'Milvus', namespaced: true }],
          })
    );

    const { result } = renderHook(() =>
      useServedKinds('cluster-a', [{ group: 'milvus.io', version: 'v1beta1', kind: 'Milvus' }])
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served?.get(servedKindKey('milvus.io', 'v1beta1', 'Milvus'))?.plural).toBe(
      'milvuses'
    );
  });

  // Plurals and scope are answered per cluster, so one cluster's answers must
  // not be carried over to another.
  it('re-reads discovery when the cluster changes, not only when the kinds do', async () => {
    mockDiscovery();

    const { result, rerender } = renderHook(({ cluster }) => useServedKinds(cluster, [apps]), {
      initialProps: { cluster: 'cluster-a' },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(request).toHaveBeenCalledWith('/apis', { cluster: 'cluster-a' }, false, true);

    rerender({ cluster: 'cluster-b' });

    await waitFor(() =>
      expect(request).toHaveBeenCalledWith('/apis', { cluster: 'cluster-b' }, false, true)
    );
  });

  it('settles instead of waiting forever when there is nothing to ask about', async () => {
    mockDiscovery();

    const { result } = renderHook(() => useServedKinds('cluster-a', []));

    // No kinds means no request, so loading has to clear on its own or the
    // caller waits on something that never happens.
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(request).not.toHaveBeenCalled();
  });

  it('clears a previous error when the inputs change', async () => {
    request.mockRejectedValueOnce(new Error('discovery unreachable'));

    const { result, rerender } = renderHook(({ cluster }) => useServedKinds(cluster, [apps]), {
      initialProps: { cluster: 'cluster-a' },
    });
    await waitFor(() => expect(result.current.error?.message).toBe('discovery unreachable'));

    mockDiscovery();
    rerender({ cluster: 'cluster-b' });

    await waitFor(() => expect(result.current.error).toBeNull());
  });

  // One group failing must not discard what every other group answered.
  it('keeps the groups that answered when one group fails', async () => {
    request.mockImplementation((path: string) => {
      switch (path) {
        case '/apis':
          return Promise.resolve({
            groups: [
              { versions: [{ groupVersion: 'apps/v1' }] },
              { versions: [{ groupVersion: 'ray.io/v1' }] },
            ],
          });
        case '/apis/apps/v1':
          return Promise.resolve({
            resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
          });
        default:
          return Promise.reject(new Error('ray.io discovery timed out'));
      }
    });

    const { result } = renderHook(() => useServedKinds('cluster-a', [apps, rayJob]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))?.plural).toBe(
      'deployments'
    );
    // The group that failed contributes no kinds, but says so rather than
    // passing for one the cluster does not have.
    expect(result.current.served?.has(servedKindKey('ray.io', 'v1', 'RayJob'))).toBe(false);
    expect(result.current.failedGroupVersions).toEqual(new Set(['ray.io/v1']));
    // Not fatal: apps/v1 answered, so there is a usable map.
    expect(result.current.error).toBeNull();
  });

  // The run for a previous cluster can still be in flight when the inputs
  // change, and its answers describe the wrong cluster.
  it('ignores a run that resolves after the cluster changed', async () => {
    let resolveFirst: (value: unknown) => void = () => {};
    const firstGroups = new Promise(resolve => {
      resolveFirst = resolve;
    });
    request.mockImplementation((path: string, params: { cluster: string }) => {
      if (params.cluster === 'cluster-a') {
        return firstGroups;
      }
      switch (path) {
        case '/apis':
          return Promise.resolve({ groups: [{ versions: [{ groupVersion: 'apps/v1' }] }] });
        default:
          return Promise.resolve({
            resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
          });
      }
    });

    const { result, rerender } = renderHook(({ cluster }) => useServedKinds(cluster, [apps]), {
      initialProps: { cluster: 'cluster-a' },
    });

    rerender({ cluster: 'cluster-b' });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))?.plural).toBe(
      'deployments'
    );

    // cluster-a's discovery finally answers, naming a different plural. It must
    // not overwrite cluster-b's answer.
    resolveFirst({ groups: [{ versions: [{ groupVersion: 'apps/v1' }] }] });
    await firstGroups;

    expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))?.plural).toBe(
      'deployments'
    );
    expect(result.current.loading).toBe(false);
  });

  // Definitions arrive in waves, so the kind set grows and the effect re-runs.
  // Re-asking /apis and the groups already answered cost ~1.3s per cluster.
  it('asks only about group/versions it has not already resolved', async () => {
    mockDiscovery();

    const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
      initialProps: { kinds: [apps] as ({ group: string; version: string; kind: string } | undefined)[] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    const callsAfterFirst = request.mock.calls.map(call => call[0]);
    expect(callsAfterFirst).toEqual(['/apis', '/apis/apps/v1']);

    // The cluster's own CRs land, adding a kind in a group not asked about yet.
    rerender({ kinds: [apps, pod] });
    await waitFor(() => expect(result.current.loading).toBe(false));

    const paths = request.mock.calls.map(call => call[0]);
    // /apis is cached, and apps/v1 was already answered: only the new group.
    expect(paths).toEqual(['/apis', '/apis/apps/v1', '/api/v1']);
    expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))?.plural).toBe(
      'deployments'
    );
    expect(result.current.served?.get(servedKindKey('', 'v1', 'Pod'))?.plural).toBe('pods');
  });

  // The kind set grows while the first run is still in flight, which is the
  // usual case: the catalog resolves, then the cluster's CRs arrive.
  it('does not re-request a group/version while the first request is in flight', async () => {
    const resolvers: Array<() => void> = [];
    request.mockImplementation((path: string) => {
      if (path === '/apis') {
        return Promise.resolve({
          groups: [
            { versions: [{ groupVersion: 'apps/v1' }] },
            { versions: [{ groupVersion: 'kubeflow.org/v1' }] },
          ],
        });
      }
      return new Promise(resolve => {
        resolvers.push(() =>
          resolve({ resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }] })
        );
      });
    });

    const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
      initialProps: { kinds: [apps] as ({ group: string; version: string; kind: string } | undefined)[] },
    });
    await waitFor(() => expect(request.mock.calls.length).toBeGreaterThan(1));

    // A second wave of definitions arrives before apps/v1 has answered.
    rerender({ kinds: [apps, mpiV1] });
    await waitFor(() =>
      expect(request.mock.calls.map(call => call[0])).toContain('/apis/kubeflow.org/v1')
    );

    resolvers.forEach(resolve => resolve());
    await waitFor(() => expect(result.current.loading).toBe(false));

    // apps/v1 asked once despite being wanted by both runs.
    const appsCalls = request.mock.calls.filter(call => call[0] === '/apis/apps/v1');
    expect(appsCalls).toHaveLength(1);
  });

  it('reports no map when discovery fails, so nothing is fetched on a guess', async () => {
    request.mockRejectedValue(new Error('discovery unreachable'));

    const { result } = renderHook(() => useServedKinds('cluster-a', [apps]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served).toBeNull();
    expect(result.current.error?.message).toBe('discovery unreachable');
  });
});
