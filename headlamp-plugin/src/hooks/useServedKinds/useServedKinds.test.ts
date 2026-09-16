// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

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
  it('reports a kind whose group is served but whose resource is not', async () => {
    mockDiscovery();

    const { result } = renderHook(() => useServedKinds([apps, mpiV1]));

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

    const { result } = renderHook(() => useServedKinds([apps, rayJob]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(request).not.toHaveBeenCalledWith('/apis/ray.io/v1', {}, false, true);
    expect(result.current.served?.has(servedKindKey('ray.io', 'v1', 'RayJob'))).toBe(false);
  });

  it('reads core kinds from /api/v1, which is not part of /apis', async () => {
    mockDiscovery();

    const { result } = renderHook(() => useServedKinds([pod]));

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
      useServedKinds([{ group: 'milvus.io', version: 'v1beta1', kind: 'Milvus' }])
    );

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served?.get(servedKindKey('milvus.io', 'v1beta1', 'Milvus'))?.plural).toBe(
      'milvuses'
    );
  });

  it('reports no map when discovery fails, so nothing is fetched on a guess', async () => {
    request.mockRejectedValue(new Error('discovery unreachable'));

    const { result } = renderHook(() => useServedKinds([apps]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.served).toBeNull();
    expect(result.current.error?.message).toBe('discovery unreachable');
  });
});
