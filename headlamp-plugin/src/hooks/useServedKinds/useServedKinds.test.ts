// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { act, renderHook, waitFor } from '@testing-library/react';
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

  // A group installed after the first /apis read must still be discovered.
  it('re-reads /apis for a group requested after the page loaded', async () => {
    let groups = [{ versions: [{ groupVersion: 'apps/v1' }] }];
    request.mockImplementation((path: string) => {
      switch (path) {
        case '/apis':
          return Promise.resolve({ groups });
        case '/apis/apps/v1':
          return Promise.resolve({
            resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
          });
        case '/apis/example.org/v1':
          return Promise.resolve({
            resources: [{ name: 'tasks', kind: 'Task', namespaced: true }],
          });
        default:
          return Promise.reject(new Error(`unexpected request: ${path}`));
      }
    });
    const task = { group: 'example.org', version: 'v1', kind: 'Task' };

    const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
      initialProps: { kinds: [apps] as ({ group: string; version: string; kind: string } | undefined)[] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    groups = [...groups, { versions: [{ groupVersion: 'example.org/v1' }] }];
    rerender({ kinds: [apps, task] });

    await waitFor(() =>
      expect(result.current.served?.get(servedKindKey('example.org', 'v1', 'Task'))).toEqual({
        plural: 'tasks',
        namespaced: true,
      })
    );
    expect(request.mock.calls.filter(call => call[0] === '/apis')).toHaveLength(2);
  });

  it('keeps the cached discovery and reports the group as failed when the refresh fails', async () => {
    let apisFails = false;
    request.mockImplementation((path: string) => {
      switch (path) {
        case '/apis':
          return apisFails
            ? Promise.reject(new Error('apis down'))
            : Promise.resolve({ groups: [{ versions: [{ groupVersion: 'apps/v1' }] }] });
        case '/apis/apps/v1':
          return Promise.resolve({
            resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
          });
        default:
          return Promise.reject(new Error(`unexpected request: ${path}`));
      }
    });
    const task = { group: 'example.org', version: 'v1', kind: 'Task' };

    const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
      initialProps: { kinds: [apps] as ({ group: string; version: string; kind: string } | undefined)[] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    apisFails = true;
    rerender({ kinds: [apps, task] });
    await waitFor(() => expect(result.current.failedGroupVersions.has('example.org/v1')).toBe(true));

    expect(result.current.error).toBeNull();
    expect(result.current.served?.has(servedKindKey('apps', 'v1', 'Deployment'))).toBe(true);
  });

  it('falls back to the last good discovery on every failed refresh', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let apisFails = false;
      request.mockImplementation((path: string) => {
        switch (path) {
          case '/apis':
            return apisFails
              ? Promise.reject(new Error('apis down'))
              : Promise.resolve({ groups: [{ versions: [{ groupVersion: 'apps/v1' }] }] });
          case '/apis/apps/v1':
            return Promise.resolve({
              resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
            });
          default:
            return Promise.reject(new Error(`unexpected request: ${path}`));
        }
      });

      const { result } = renderHook(() => useServedKinds('cluster-a', [apps, rayJob]));
      await waitFor(() => expect(result.current.loading).toBe(false));

      apisFails = true;
      const apisCalls = () => request.mock.calls.filter(call => call[0] === '/apis').length;
      for (const attempt of [2, 3]) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(31_000);
        });
        await waitFor(() => expect(apisCalls()).toBeGreaterThanOrEqual(attempt));
        await waitFor(() => expect(result.current.failedGroupVersions.has('ray.io/v1')).toBe(true));
        expect(result.current.error).toBeNull();
        expect(result.current.served?.has(servedKindKey('apps', 'v1', 'Deployment'))).toBe(true);
      }
    } finally {
      vi.useRealTimers();
    }
  });

  it('clears a failure once a later /apis answer shows the group is absent', async () => {
    let apisFails = false;
    request.mockImplementation((path: string) => {
      switch (path) {
        case '/apis':
          return apisFails
            ? Promise.reject(new Error('apis down'))
            : Promise.resolve({ groups: [{ versions: [{ groupVersion: 'apps/v1' }] }] });
        case '/apis/apps/v1':
          return Promise.resolve({
            resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
          });
        case '/api/v1':
          return Promise.resolve({ resources: [{ name: 'pods', kind: 'Pod', namespaced: true }] });
        default:
          return Promise.reject(new Error(`unexpected request: ${path}`));
      }
    });
    const task = { group: 'example.org', version: 'v1', kind: 'Task' };

    const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
      initialProps: { kinds: [apps] as ({ group: string; version: string; kind: string } | undefined)[] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    apisFails = true;
    rerender({ kinds: [apps, task] });
    await waitFor(() => expect(result.current.failedGroupVersions.has('example.org/v1')).toBe(true));

    apisFails = false;
    rerender({ kinds: [apps, task, pod] });
    await waitFor(() => expect(result.current.served?.has(servedKindKey('', 'v1', 'Pod'))).toBe(true));

    expect(result.current.failedGroupVersions.size).toBe(0);
  });

  it('does not re-read /apis for a group that is still absent', async () => {
    mockDiscovery();

    const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
      initialProps: { kinds: [apps, rayJob] as ({ group: string; version: string; kind: string } | undefined)[] },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    rerender({ kinds: [apps, rayJob, pod] });
    await waitFor(() => expect(result.current.served?.has(servedKindKey('', 'v1', 'Pod'))).toBe(true));

    // Nothing newly requested can be missing, so /apis is not read again.
    expect(request.mock.calls.filter(call => call[0] === '/apis')).toHaveLength(1);
  });

  it('rechecks an absent group after expiry even when the requested groups stay the same', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let groups = [{ versions: [{ groupVersion: 'apps/v1' }] }];
      request.mockImplementation((path: string) => {
        switch (path) {
          case '/apis':
            return Promise.resolve({ groups });
          case '/apis/apps/v1':
            return Promise.resolve({
              resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
            });
          case '/apis/ray.io/v1':
            return Promise.resolve({
              resources: [{ name: 'rayjobs', kind: 'RayJob', namespaced: true }],
            });
          default:
            return Promise.reject(new Error(`unexpected request: ${path}`));
        }
      });

      const loadings: boolean[] = [];
      const { result } = renderHook(() => {
        const value = useServedKinds('cluster-a', [apps, rayJob]);
        loadings.push(value.loading);
        return value;
      });
      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(result.current.served?.has(servedKindKey('ray.io', 'v1', 'RayJob'))).toBe(false);

      groups = [...groups, { versions: [{ groupVersion: 'ray.io/v1' }] }];
      const settled = loadings.length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000);
      });
      expect(loadings.slice(settled)).not.toContain(true);

      await waitFor(() =>
        expect(result.current.served?.get(servedKindKey('ray.io', 'v1', 'RayJob'))?.plural).toBe(
          'rayjobs'
        )
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps requests bounded while a listed group keeps failing its resource request', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let groups = [{ versions: [{ groupVersion: 'apps/v1' }] }];
      request.mockImplementation((path: string) => {
        switch (path) {
          case '/apis':
            return Promise.resolve({ groups });
          case '/apis/apps/v1':
            return Promise.resolve({
              resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
            });
          default:
            return Promise.reject(new Error(`unavailable: ${path}`));
        }
      });

      const { result } = renderHook(() => useServedKinds('cluster-a', [apps, rayJob]));
      await waitFor(() => expect(result.current.loading).toBe(false));

      groups = [...groups, { versions: [{ groupVersion: 'ray.io/v1' }] }];
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000);
      });
      // Small steps, so a retry with no delay would show as a burst of requests.
      for (let i = 0; i < 100; i++) {
        await act(async () => {
          await vi.advanceTimersByTimeAsync(1);
        });
      }
      await act(async () => {
        await vi.advanceTimersByTimeAsync(40_000);
      });

      const rayCalls = request.mock.calls.filter(call => call[0] === '/apis/ray.io/v1').length;
      expect(rayCalls).toBeGreaterThan(1);
      expect(rayCalls).toBeLessThanOrEqual(6);
      expect(result.current.failedGroupVersions.has('ray.io/v1')).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('asks about an absent group again once its answer has expired', async () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    try {
      let groups = [{ versions: [{ groupVersion: 'apps/v1' }] }];
      request.mockImplementation((path: string) => {
        switch (path) {
          case '/apis':
            return Promise.resolve({ groups });
          case '/apis/apps/v1':
            return Promise.resolve({
              resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
            });
          case '/apis/ray.io/v1':
            return Promise.resolve({
              resources: [{ name: 'rayjobs', kind: 'RayJob', namespaced: true }],
            });
          default:
            return Promise.reject(new Error(`unexpected request: ${path}`));
        }
      });

      const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
        initialProps: { kinds: [apps, rayJob] as ({ group: string; version: string; kind: string } | undefined)[] },
      });
      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(result.current.served?.has(servedKindKey('ray.io', 'v1', 'RayJob'))).toBe(false);

      // ray.io is now installed and the earlier answer has expired.
      groups = [...groups, { versions: [{ groupVersion: 'ray.io/v1' }] }];
      vi.setSystemTime(Date.now() + 60_000);
      rerender({ kinds: [apps, rayJob, pod] });

      await waitFor(() =>
        expect(result.current.served?.get(servedKindKey('ray.io', 'v1', 'RayJob'))?.plural).toBe(
          'rayjobs'
        )
      );
    } finally {
      vi.useRealTimers();
    }
  });

  // A cached rejection would answer every later wave with the old failure, so
  // one hiccup would leave discovery dead for the life of the page.
  it('asks again after /apis fails, rather than caching the failure', async () => {
    let attempts = 0;
    request.mockImplementation((path: string) => {
      if (path === '/apis') {
        attempts++;
        return attempts === 1
          ? Promise.reject(new Error('api hiccup'))
          : Promise.resolve({ groups: [{ versions: [{ groupVersion: 'apps/v1' }] }] });
      }
      return Promise.resolve({
        resources: [{ name: 'deployments', kind: 'Deployment', namespaced: true }],
      });
    });

    const { result, rerender } = renderHook(({ kinds }) => useServedKinds('cluster-a', kinds), {
      initialProps: { kinds: [apps] as ({ group: string; version: string; kind: string } | undefined)[] },
    });
    await waitFor(() => expect(result.current.error?.message).toBe('api hiccup'));

    // A later wave of definitions arrives once the network has recovered.
    rerender({ kinds: [apps, mpiV1] });

    await waitFor(() =>
      expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))?.plural).toBe(
        'deployments'
      )
    );
    expect(result.current.error).toBeNull();
    expect(attempts).toBe(2);
  });

  // Latent while a component mounts per cluster, but the hook takes a cluster
  // and a late answer must not write into whatever cache is current when it
  // lands.
  it('writes a late answer into the cache its request was made against', async () => {
    let releaseA: () => void = () => {};
    request.mockImplementation((path: string, params: { cluster: string }) => {
      if (path === '/apis') {
        return Promise.resolve({
          groups: [
            { versions: [{ groupVersion: 'apps/v1' }] },
            { versions: [{ groupVersion: 'kubeflow.org/v1' }] },
          ],
        });
      }
      if (path === '/apis/kubeflow.org/v1') {
        return Promise.resolve({
          resources: [{ name: 'pytorchjobs', kind: 'PyTorchJob', namespaced: true }],
        });
      }
      if (params.cluster === 'cluster-a') {
        return new Promise(resolve => {
          releaseA = () =>
            resolve({ resources: [{ name: 'a-plurals', kind: 'Deployment', namespaced: true }] });
        });
      }
      return Promise.resolve({
        resources: [{ name: 'b-plurals', kind: 'Deployment', namespaced: true }],
      });
    });

    const { result, rerender } = renderHook(
      ({ cluster, kinds }) => useServedKinds(cluster, kinds),
      {
        initialProps: {
          cluster: 'cluster-a',
          kinds: [apps] as ({ group: string; version: string; kind: string } | undefined)[],
        },
      }
    );
    await waitFor(() => expect(request).toHaveBeenCalledTimes(2));

    rerender({ cluster: 'cluster-b', kinds: [apps] });
    await waitFor(() =>
      expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))?.plural).toBe(
        'b-plurals'
      )
    );

    // cluster-a finally answers. Its run is cancelled, so this cannot reach
    // state directly: the damage is to the cache it writes into.
    releaseA();
    await new Promise(resolve => setTimeout(resolve, 20));

    // A later wave for cluster-b re-emits from that cache. Waiting on the kind
    // only this wave can produce proves the re-emit happened before asserting.
    rerender({ cluster: 'cluster-b', kinds: [apps, mpiV1] });
    await waitFor(() =>
      expect(result.current.served?.has(servedKindKey('kubeflow.org', 'v1', 'PyTorchJob'))).toBe(
        true
      )
    );
    expect(result.current.served?.get(servedKindKey('apps', 'v1', 'Deployment'))?.plural).toBe(
      'b-plurals'
    );
  });

  it('reports no map when discovery fails, so nothing is fetched on a guess', async () => {
    request.mockRejectedValue(new Error('discovery unreachable'));

    const { result } = renderHook(() => useServedKinds('cluster-a', [apps]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served).toBeNull();
    expect(result.current.error?.message).toBe('discovery unreachable');
  });
});
