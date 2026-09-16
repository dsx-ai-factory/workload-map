// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { useKartaWasm, useKartaDefinitions, useServedKinds } = vi.hoisted(() => ({
  useKartaWasm: vi.fn(),
  useKartaDefinitions: vi.fn(),
  useServedKinds: vi.fn(),
}));
vi.mock('../useKartaWasm', () => ({ useKartaWasm }));
vi.mock('../useKartaDefinitions', () => ({ useKartaDefinitions }));
vi.mock('../useServedKinds', () => ({
  useServedKinds,
  servedKindKey: (group: string, version: string, kind: string) =>
    `${group ? `${group}/${version}` : version}/${kind}`,
}));

// Serves whatever the test's definitions ask for, so the tests that are not
// about discovery keep exercising every definition they declare.
function servesEverything() {
  useServedKinds.mockImplementation((kinds: ({ group: string; version: string; kind: string } | undefined)[]) => ({
    served: new Map(
      kinds
        .filter(k => !!k)
        .map(k => [
          `${k.group ? `${k.group}/${k.version}` : k.version}/${k.kind}`,
          { plural: `${k.kind.toLowerCase()}s`, namespaced: true },
        ])
    ),
    loading: false,
    error: null,
  }));
}

beforeEach(() => {
  servesEverything();
});

vi.mock('./KindFetcher', () => ({
  KindFetcher: ({ definition, onRows, onError }: any) => {
    useEffect(() => {
      const name = definition.karta.metadata.name;
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

  it('surfaces a per-kind list error when the engine itself is fine', async () => {
    useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
    useKartaDefinitions.mockReturnValue({
      definitions: [definition('broken')],
      loading: false,
      error: null,
      installed: true,
      crdMissing: false,
    });

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.error?.message).toBe('broken failed to list'));
  });
});
