// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { act, render, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

interface ClusterPlan {
  loading?: boolean;
  error?: Error | null;
  warning?: Error | null;
  kinds?: string[];
  failing?: string[];
  pending?: string[];
  discoveryFailures?: Record<string, Error>;
}

const { useKartaWasm, clustersRef, currentRef, namespacesRef, plans } = vi.hoisted(() => ({
  useKartaWasm: vi.fn(),
  clustersRef: { current: ['cluster-a'] as string[] },
  currentRef: { current: 'cluster-a' as string | null },
  namespacesRef: { current: undefined as string[] | undefined },
  plans: {} as Record<string, ClusterPlan>,
}));

vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  K8s: {
    useSelectedClusters: () => clustersRef.current,
    useCluster: () => currentRef.current,
  },
}));
vi.mock('../useKartaWasm/useKartaWasm', () => ({ useKartaWasm }));
// Reads Headlamp's redux store, which these tests do not set up.
vi.mock('../useSelectedNamespaces/useSelectedNamespaces', () => ({
  useSelectedNamespaces: () => namespacesRef.current,
}));

// Stands in for one cluster's definitions and discovery, which ClusterFetcher
// owns and its own tests cover. Each test declares what a cluster reports.
vi.mock('./ClusterFetcher/ClusterFetcher', () => ({
  ClusterFetcher: ({ cluster, namespaces, onRows, onError, onState }: any) => {
    const selection = (namespaces ?? []).join(',');
    useEffect(() => {
      const plan = plans[cluster] ?? {};
      const kinds = plan.kinds ?? [];
      onState(cluster, {
        loading: plan.loading ?? false,
        error: plan.error ?? null,
        warning: plan.warning ?? null,
        expectedKinds: kinds,
        discoveryFailures: plan.discoveryFailures ?? {},
      });
      for (const kind of kinds) {
        if (plan.failing?.includes(kind)) {
          onError(cluster, kind, new Error(`${kind} failed to list`));
        } else if (!plan.pending?.includes(kind)) {
          onRows(cluster, kind, [{ id: `${cluster}/${kind}`, name: kind }]);
        }
      }
      // Re-reports when the selection changes, as the real one does.
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [selection]);
    return null;
  },
}));

import type { UseWorkloadRowsResult } from './useWorkloadRows';
import { useWorkloadRows } from './useWorkloadRows';

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

beforeEach(() => {
  clustersRef.current = ['cluster-a'];
  currentRef.current = 'cluster-a';
  namespacesRef.current = undefined;
  for (const key of Object.keys(plans)) {
    delete plans[key];
  }
  useKartaWasm.mockReturnValue({ karta: {}, loading: false, error: null });
});

describe('useWorkloadRows', () => {
  it('reports loading while the engine is still loading', () => {
    useKartaWasm.mockReturnValue({ karta: null, loading: true, error: null });
    plans['cluster-a'] = { kinds: [] };

    const harness = renderHarness();

    expect(harness.current.loading).toBe(true);
    expect(harness.current.rows).toBeNull();
  });

  it('aggregates rows from every selected cluster', async () => {
    clustersRef.current = ['cluster-a', 'cluster-b'];
    plans['cluster-a'] = { kinds: ['deployment'] };
    plans['cluster-b'] = { kinds: ['deployment', 'pytorchjob'] };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.rows).toHaveLength(3));
    expect(harness.current.rows).toEqual(
      expect.arrayContaining([
        { id: 'cluster-a/deployment', name: 'deployment' },
        { id: 'cluster-b/deployment', name: 'deployment' },
        { id: 'cluster-b/pytorchjob', name: 'pytorchjob' },
      ])
    );
    expect(harness.current.error).toBeNull();
  });

  // The same definition name serves every cluster, so rows must not be read
  // across them.
  it('keeps same-named kinds on different clusters apart', async () => {
    clustersRef.current = ['cluster-a', 'cluster-b'];
    plans['cluster-a'] = { kinds: ['deployment'] };
    plans['cluster-b'] = { kinds: ['deployment'] };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.rows).toHaveLength(2));
    expect(new Set(harness.current.rows?.map(row => row.id)).size).toBe(2);
  });

  it('stays loading until every cluster has reported', async () => {
    clustersRef.current = ['cluster-a', 'cluster-b'];
    plans['cluster-a'] = { kinds: ['deployment'] };
    plans['cluster-b'] = { kinds: ['slow'], pending: ['slow'] };

    const harness = renderHarness();

    // cluster-a is done, but cluster-b's kind never reports.
    expect(harness.current.loading).toBe(true);
    expect(harness.current.rows).toBeNull();
  });

  it('stays loading while a cluster is still resolving its definitions', () => {
    clustersRef.current = ['cluster-a', 'cluster-b'];
    plans['cluster-a'] = { kinds: ['deployment'] };
    plans['cluster-b'] = { loading: true, kinds: [] };

    const harness = renderHarness();

    expect(harness.current.loading).toBe(true);
  });

  // One cluster being unreachable must not hide the clusters that answered.
  it('reports a failing cluster beside the results of the others', async () => {
    clustersRef.current = ['cluster-a', 'cluster-b'];
    plans['cluster-a'] = { kinds: ['deployment'] };
    plans['cluster-b'] = { kinds: [], error: new Error('cluster-b unreachable') };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.rows).toEqual([{ id: 'cluster-a/deployment', name: 'deployment' }]);
    expect(harness.current.error).toBeNull();
    expect(harness.current.errorsByCluster['cluster-b']?.message).toBe('cluster-b unreachable');
  });

  it('is fatal only when every cluster failed', async () => {
    clustersRef.current = ['cluster-a', 'cluster-b'];
    plans['cluster-a'] = { kinds: [], error: new Error('cluster-a unreachable') };
    plans['cluster-b'] = { kinds: [], error: new Error('cluster-b unreachable') };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.error?.message).toBe('cluster-a unreachable');
  });

  it('reports a per-kind list error without making it fatal', async () => {
    plans['cluster-a'] = { kinds: ['broken', 'deployment'], failing: ['broken'] };

    const harness = renderHarness();

    await waitFor(() =>
      expect(harness.current.errorsByKind['cluster-a/broken']?.message).toBe(
        'broken failed to list'
      )
    );
    expect(harness.current.error).toBeNull();
    expect(harness.current.rows).toEqual([{ id: 'cluster-a/deployment', name: 'deployment' }]);
  });

  it('reports a kind whose group discovery failed, naming its cluster', async () => {
    plans['cluster-a'] = {
      kinds: ['deployment'],
      discoveryFailures: { rayjob: new Error('discovery failed for ray.io/v1') },
    };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.errorsByKind['cluster-a/rayjob']?.message).toBe(
      'discovery failed for ray.io/v1'
    );
    expect(harness.current.error).toBeNull();
  });

  // useSelectedClusters is empty unless clusters are explicitly selected,
  // which is the ordinary single-cluster case.
  it('falls back to the current cluster when none are explicitly selected', async () => {
    clustersRef.current = [];
    currentRef.current = 'cluster-a';
    plans['cluster-a'] = { kinds: ['deployment'] };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.rows).toEqual([{ id: 'cluster-a/deployment', name: 'deployment' }]);
  });

  // Discovery names the same kinds before any list resolves, so a deselected
  // cluster's rows would pass the loading gate on re-selection, and stay for
  // good if the new list failed.
  it('drops a deselected cluster rows rather than reusing them when it returns', async () => {
    clustersRef.current = ['cluster-a', 'cluster-b'];
    plans['cluster-a'] = { kinds: ['deployment'] };
    plans['cluster-b'] = { kinds: ['deployment'] };

    const harness = renderHarness();
    await waitFor(() => expect(harness.current.rows).toHaveLength(2));

    // cluster-a goes away.
    clustersRef.current = ['cluster-b'];
    harness.rerender();
    await waitFor(() => expect(harness.current.rows).toHaveLength(1));

    // It comes back, and its list has not resolved yet.
    plans['cluster-a'] = { kinds: ['deployment'], pending: ['deployment'] };
    clustersRef.current = ['cluster-a', 'cluster-b'];
    harness.rerender();

    await waitFor(() => expect(harness.current.loading).toBe(true));
    expect(harness.current.rows).toBeNull();
  });

  // The catalog still describes kinds without permission on kartas.run.ai, so
  // the rows show and the CR failure travels in the partial channel.
  it('shows rows and reports the cluster when its CRs are unreadable', async () => {
    plans['cluster-a'] = { kinds: ['deployment'], warning: new Error('kartas forbidden') };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.rows).toEqual([{ id: 'cluster-a/deployment', name: 'deployment' }]);
    expect(harness.current.error).toBeNull();
    expect(harness.current.warningsByCluster['cluster-a']?.message).toBe('kartas forbidden');
    expect(harness.current.errorsByCluster['cluster-a']).toBeUndefined();
  });

  // Namespace a's rows would otherwise satisfy the loading gate under a
  // b-only selection, and stay on screen when b's list fails.
  it('drops rows from a previous namespace selection', async () => {
    namespacesRef.current = ['namespace-a'];
    plans['cluster-a'] = { kinds: ['deployment'] };

    const harness = renderHarness();
    await waitFor(() => expect(harness.current.rows).toHaveLength(1));

    // The selection changes and the new list fails for that kind.
    namespacesRef.current = ['namespace-b'];
    plans['cluster-a'] = { kinds: ['deployment'], failing: ['deployment'] };
    harness.rerender();

    await waitFor(() =>
      expect(harness.current.errorsByKind['cluster-a/deployment']?.message).toBe(
        'deployment failed to list'
      )
    );
    // Not namespace-a's row.
    expect(harness.current.rows).toEqual([]);
  });

  // Scoped keys stop one selection reading another's, but a selection returned
  // to would read its own stale entries: they satisfy the loading gate before
  // any list resolves, so a workload deleted meanwhile renders as loaded.
  it('drops entries from a selection that is returned to', async () => {
    namespacesRef.current = ['namespace-a'];
    plans['cluster-a'] = { kinds: ['deployment'] };

    const harness = renderHarness();
    await waitFor(() => expect(harness.current.rows).toHaveLength(1));

    // Away, then back, with namespace-a's list now failing to resolve.
    namespacesRef.current = ['namespace-b'];
    harness.rerender();
    await waitFor(() => expect(harness.current.rows).toHaveLength(1));

    namespacesRef.current = ['namespace-a'];
    plans['cluster-a'] = { kinds: ['deployment'], pending: ['deployment'] };
    harness.rerender();

    // Nothing has reported for this selection, so there is nothing to show.
    await waitFor(() => expect(harness.current.loading).toBe(true));
    expect(harness.current.rows).toBeNull();
  });

  it('loads the engine again when a retry is asked for', async () => {
    useKartaWasm.mockReturnValue({ karta: null, loading: false, error: new Error('wasm failed') });
    plans['cluster-a'] = { kinds: [] };

    const harness = renderHarness();
    await waitFor(() => expect(harness.current.engineError).not.toBeNull());
    const attemptsBefore = useKartaWasm.mock.calls.length;

    act(() => harness.current.retryEngine());

    // Called again with a new attempt, which is what re-runs the load.
    expect(useKartaWasm.mock.calls.length).toBeGreaterThan(attemptsBefore);
    expect(useKartaWasm.mock.calls.at(-1)?.[0]).toBe(1);
  });

  // listCatalog reads through the engine, so a failed load knocks out every
  // cluster's definitions. Blaming the cluster would point at the wrong thing
  // and say it twice.
  it('does not blame a cluster for the engine failing to load', async () => {
    const wasm = new Error('wasm load failed');
    useKartaWasm.mockReturnValue({ karta: null, loading: false, error: wasm });
    // A catalog-only cluster: no definitions survive, so it reports fatally.
    plans['cluster-a'] = { kinds: [], error: new Error('wasm load failed') };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.engineError?.message).toBe('wasm load failed');
    expect(harness.current.errorsByCluster).toEqual({});
    expect(harness.current.warningsByCluster).toEqual({});
  });

  it('still reports a cluster failure unrelated to the engine', async () => {
    useKartaWasm.mockReturnValue({
      karta: null,
      loading: false,
      error: new Error('wasm load failed'),
    });
    plans['cluster-a'] = { kinds: [], error: new Error('cluster-a unreachable') };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.errorsByCluster['cluster-a']?.message).toBe('cluster-a unreachable');
  });

  // Rows are metadata and need no engine: only the status column does. A
  // failed 19MB fetch must not blank a table that would have been full.
  it('reports an engine failure without hiding the rows', async () => {
    useKartaWasm.mockReturnValue({
      karta: null,
      loading: false,
      error: new Error('wasm load failed'),
    });
    plans['cluster-a'] = { kinds: ['deployment'] };

    const harness = renderHarness();

    await waitFor(() => expect(harness.current.loading).toBe(false));
    expect(harness.current.engineError?.message).toBe('wasm load failed');
    expect(harness.current.error).toBeNull();
    expect(harness.current.rows).toEqual([{ id: 'cluster-a/deployment', name: 'deployment' }]);
  });
});
