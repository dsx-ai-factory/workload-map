// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

const { useListMock, listCatalog } = vi.hoisted(() => ({
  useListMock: vi.fn(),
  listCatalog: vi.fn(),
}));
vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  K8s: { crd: { makeCustomResourceClass: vi.fn(() => ({ useList: useListMock })) } },
}));
vi.mock('../../lib/karta/kartaUtil', () => ({ listCatalog }));

import type { Karta } from '../../lib/karta/karta.types';
import { useKartaDefinitions } from './useKartaDefinitions';

function karta(name: string, kind?: { group: string; version: string; kind: string }): Karta {
  return {
    apiVersion: 'run.ai/v1alpha1',
    kind: 'Karta',
    metadata: { name },
    spec: { structureDefinition: { rootComponent: { kind } } },
  };
}

const deploymentGVK = { group: 'apps', version: 'v1', kind: 'Deployment' };

describe('useKartaDefinitions', () => {
  it('falls back to catalog-only definitions when the CRD is missing (404)', async () => {
    const catalogDeployment = karta('catalog-deployment', deploymentGVK);
    listCatalog.mockResolvedValue([catalogDeployment]);
    useListMock.mockReturnValue([null, { status: 404 }]);

    const { result } = renderHook(() => useKartaDefinitions());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.crdMissing).toBe(true);
    expect(result.current.installed).toBe(false);
    expect(result.current.error).toBeNull();
    expect(result.current.definitions).toEqual([{ karta: catalogDeployment, origin: 'catalog' }]);
  });

  it('stays loading, and does not claim installed, while the cluster list is pending', async () => {
    listCatalog.mockResolvedValue([karta('catalog-deployment', deploymentGVK)]);
    useListMock.mockReturnValue([null, null]);

    const { result } = renderHook(() => useKartaDefinitions());

    await waitFor(() => expect(listCatalog).toHaveBeenCalled());
    expect(result.current.loading).toBe(true);
    expect(result.current.installed).toBe(false);
    expect(result.current.crdMissing).toBe(false);
  });

  it('reports installed=true and merges cluster CRs when the list succeeds', async () => {
    const catalogDeployment = karta('catalog-deployment', deploymentGVK);
    const clusterDeployment = karta('cluster-deployment', deploymentGVK);
    listCatalog.mockResolvedValue([catalogDeployment]);
    useListMock.mockReturnValue([[{ jsonData: clusterDeployment }], null]);

    const { result } = renderHook(() => useKartaDefinitions());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.installed).toBe(true);
    expect(result.current.crdMissing).toBe(false);
    expect(result.current.definitions).toEqual([{ karta: clusterDeployment, origin: 'cluster' }]);
  });

  it('surfaces a non-404 list error instead of silently swallowing it', async () => {
    listCatalog.mockResolvedValue([]);
    useListMock.mockReturnValue([null, { status: 403, message: 'forbidden' }]);

    const { result } = renderHook(() => useKartaDefinitions());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.crdMissing).toBe(false);
    expect(result.current.error).toEqual({ status: 403, message: 'forbidden' });
  });

  it('surfaces a catalog loading error', async () => {
    listCatalog.mockRejectedValue(new Error('wasm not loaded'));
    useListMock.mockReturnValue([[], null]);

    const { result } = renderHook(() => useKartaDefinitions());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error?.message).toBe('wasm not loaded');
    expect(result.current.definitions).toEqual([]);
  });
});
