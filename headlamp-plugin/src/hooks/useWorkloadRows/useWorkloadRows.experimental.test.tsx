// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { act, render, waitFor } from '@testing-library/react';
import { useEffect, useSyncExternalStore } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Karta } from '../../lib/karta/karta.types';
import type { KindFetcherProps } from './KindFetcher/KindFetcher';
import { useWorkloadRows, UseWorkloadRowsResult } from './useWorkloadRows';

const { request, useList, listCatalog, setting, listeners, mounted, pending } = vi.hoisted(() => ({
  request: vi.fn(),
  useList: vi.fn(),
  listCatalog: vi.fn(),
  setting: { current: undefined as { enableExperimental: boolean } | undefined },
  listeners: new Set<() => void>(),
  mounted: new Set<string>(),
  pending: new Set<string>(),
}));

vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  ApiProxy: { request },
  K8s: {
    useSelectedClusters: () => ['cluster-a'],
    useCluster: () => 'cluster-a',
    cluster: { getAllowedNamespaces: () => [] },
    crd: { makeCustomResourceClass: () => ({ useList }) },
  },
}));
vi.mock('../../lib/karta/kartaUtil', () => ({ listCatalog }));
vi.mock('../useKartaWasm/useKartaWasm', () => ({
  useKartaWasm: () => ({ loading: false, error: null }),
}));
vi.mock('../useSelectedNamespaces/useSelectedNamespaces', () => ({
  useSelectedNamespaces: () => undefined,
}));
vi.mock('../../config', () => ({
  useKartaConfig: () =>
    useSyncExternalStore(
      listener => {
        listeners.add(listener);
        return () => listeners.delete(listener);
      },
      () => setting.current
    ),
}));

// Keep definitions, discovery, and aggregation real. This boundary stands in
// for individual Kubernetes lists so a remounted list can remain pending.
vi.mock('./KindFetcher/KindFetcher', () => ({
  KindFetcher: ({ definition, onRows, onError }: KindFetcherProps) => {
    const name = definition.karta.metadata.name;
    useEffect(() => {
      mounted.add(name);
      if (!pending.has(name)) {
        if (name === 'broken-preview') {
          onError(name, new Error('forbidden'));
        } else {
          onRows(name, [{ id: name, name } as any]);
        }
      }
      return () => {
        mounted.delete(name);
      };
    }, [name, onRows, onError]);
    return null;
  },
}));

function definition(name: string, group: string, experimental = false): Karta {
  return {
    apiVersion: 'run.ai/v1alpha1',
    kind: 'Karta',
    metadata: { name },
    spec: {
      experimental,
      structureDefinition: { rootComponent: { kind: { group, version: 'v1', kind: name } } },
    },
  };
}

function Harness({ onResult }: { onResult: (result: UseWorkloadRowsResult) => void }) {
  const result = useWorkloadRows();
  onResult(result);
  return <>{result.fetchers}</>;
}

function setExperimental(enabled: boolean) {
  act(() => {
    setting.current = { enableExperimental: enabled };
    listeners.forEach(listener => listener());
  });
}

beforeEach(() => {
  setting.current = undefined;
  mounted.clear();
  pending.clear();
  request.mockReset();
  listCatalog.mockResolvedValue([
    definition('stable', 'apps'),
    definition('preview', 'preview.example', true),
  ]);
  useList.mockReturnValue([
    [{ jsonData: definition('broken-preview', 'broken.example', true) }],
    null,
  ]);
  request.mockImplementation(async (path: string) => {
    if (path === '/apis') {
      return {
        groups: ['apps/v1', 'preview.example/v1', 'broken.example/v1'].map(groupVersion => ({
          versions: [{ groupVersion }],
        })),
      };
    }
    const kinds: Record<string, string> = {
      '/apis/apps/v1': 'stable',
      '/apis/preview.example/v1': 'preview',
      '/apis/broken.example/v1': 'broken-preview',
    };
    return { resources: [{ name: kinds[path], kind: kinds[path], namespaced: true }] };
  });
});

describe('experimental workload definitions', () => {
  it('stays quiet by default and removes fetchers, rows, and errors when disabled', async () => {
    let latest: UseWorkloadRowsResult;
    render(<Harness onResult={result => (latest = result)} />);

    await waitFor(() => expect(latest.rows?.map(row => row.name)).toEqual(['stable']));
    expect([...mounted]).toEqual(['stable']);
    expect(request.mock.calls.map(([path]) => path)).toEqual(['/apis', '/apis/apps/v1']);
    expect(latest.errorsByKind).toEqual({});

    setExperimental(true);
    await waitFor(() => expect(latest.rows?.map(row => row.name)).toEqual(['stable', 'preview']));
    expect(mounted).toEqual(new Set(['stable', 'preview', 'broken-preview']));
    expect(request).toHaveBeenCalledWith('/apis/preview.example/v1', expect.anything(), false, true);
    expect(request).toHaveBeenCalledWith('/apis/broken.example/v1', expect.anything(), false, true);
    expect(latest.errorsByKind['cluster-a/broken-preview']?.message).toBe('forbidden');

    setExperimental(false);
    await waitFor(() => expect(latest.rows?.map(row => row.name)).toEqual(['stable']));
    expect([...mounted]).toEqual(['stable']);
    expect(latest.errorsByKind).toEqual({});

    pending.add('preview');
    pending.add('broken-preview');
    setExperimental(true);
    await waitFor(() => expect(mounted.size).toBe(3));
    expect(latest.loading).toBe(true);
    expect(latest.rows).toBeNull();
    expect(latest.errorsByKind).toEqual({});
  });
});
