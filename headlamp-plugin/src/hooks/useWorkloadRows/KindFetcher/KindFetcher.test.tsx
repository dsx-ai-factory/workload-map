// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { useListMock, evaluatePhases } = vi.hoisted(() => ({
  useListMock: vi.fn(),
  evaluatePhases: vi.fn(),
}));
vi.mock('@kinvolk/headlamp-plugin/lib', () => ({
  K8s: { crd: { makeCustomResourceClass: vi.fn(() => ({ useList: useListMock })) } },
}));
vi.mock('../../../lib/karta/kartaUtil', () => ({ evaluatePhases }));

import type { Definition } from '../../../lib/karta/definitions';
import { KindFetcher } from './KindFetcher';

const deploymentGVK = { group: 'apps', version: 'v1', kind: 'Deployment' };

function definition(resourceVersion?: string): Definition {
  return {
    karta: {
      apiVersion: 'run.ai/v1alpha1',
      kind: 'Karta',
      metadata: { name: 'deployment', resourceVersion },
      spec: { structureDefinition: { rootComponent: { kind: deploymentGVK } } },
    },
    origin: 'catalog',
  };
}

function item(resourceVersion: string) {
  return {
    cluster: 'local',
    jsonData: {
      apiVersion: 'apps/v1',
      kind: 'Deployment',
      metadata: { name: 'api', namespace: 'default', uid: 'uid-1', resourceVersion },
    },
  };
}

// useList returns a tuple that also carries the query fields, so a mocked
// result has to be both to exercise the loading gate.
function listResult(items: unknown[] | null, error: unknown, isLoading = false) {
  const result: any = [items, error];
  result.items = items;
  result.isLoading = isLoading;
  return result;
}

describe('KindFetcher', () => {
  beforeEach(() => {
    evaluatePhases.mockReset();
    useListMock.mockReset();
  });

  // Without the cluster, useList spans every selected cluster, and those
  // workloads would be read through this cluster's definitions.
  it('lists the workloads of the given cluster only', () => {
    useListMock.mockReturnValue([[], null]);

    render(
      <KindFetcher
        definition={definition()}
        cluster="cluster-a"
        plural="reactors"
        namespaced
        onRows={vi.fn()}
        onError={vi.fn()}
      />
    );

    expect(useListMock).toHaveBeenCalledWith({ cluster: 'cluster-a' });
  });

  it('computes status once per workload and reuses it when resourceVersion is unchanged', async () => {
    evaluatePhases.mockResolvedValue(['Running']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Running'] })])
    );

    // Same resourceVersion, new items array reference (a poll tick) — must
    // not recompute status.
    useListMock.mockReturnValue([[item('1')], null]);
    rerender(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(onRows).toHaveBeenLastCalledWith('deployment', expect.any(Array)));
    expect(evaluatePhases).toHaveBeenCalledTimes(1);
  });

  it('recomputes status when resourceVersion changes', async () => {
    evaluatePhases.mockResolvedValueOnce(['Running']).mockResolvedValueOnce(['Failed']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));

    useListMock.mockReturnValue([[item('2')], null]);
    rerender(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Failed'] })])
    );
  });

  it('discards a stale evaluatePhases result that resolves after a newer one', async () => {
    let resolveV1: (phases: string[]) => void = () => {};
    const v1Promise = new Promise<string[]>(resolve => {
      resolveV1 = resolve;
    });
    evaluatePhases.mockReturnValueOnce(v1Promise).mockResolvedValueOnce(['Initializing']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));

    // A newer resourceVersion supersedes the still-pending v1 call before it
    // resolves.
    useListMock.mockReturnValue([[item('2')], null]);
    rerender(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Initializing'] })])
    );

    // The stale v1 call finally resolves — must not overwrite the cache
    // with ["Running"] now that v2's ["Initializing"] is current.
    resolveV1(['Running']);
    await v1Promise;

    useListMock.mockReturnValue([[item('2')], null]);
    rerender(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Initializing'] })])
    );
  });

  // The same workload yields different phases under an edited definition, so
  // the cache cannot survive the change.
  it('recomputes status when the definition changes but the workload does not', async () => {
    evaluatePhases.mockResolvedValueOnce(['Running']).mockResolvedValueOnce(['Degraded']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition('10')} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));

    useListMock.mockReturnValue([[item('1')], null]);
    rerender(<KindFetcher definition={definition('11')} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Degraded'] })])
    );
  });

  // useKartaDefinitions rebuilds its Definition objects every render, so a new
  // object with unchanged content must not invalidate anything.
  it('keeps cached status when an unchanged definition arrives as a new object', async () => {
    evaluatePhases.mockResolvedValue(['Running']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition('10')} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));

    rerender(<KindFetcher definition={definition('10')} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(onRows).toHaveBeenLastCalledWith('deployment', expect.any(Array)));
    expect(evaluatePhases).toHaveBeenCalledTimes(1);
  });

  // Otherwise a long-lived page accumulates an entry per workload it has ever
  // seen, which job churn makes unbounded.
  it('drops cached status for workloads that are no longer listed', async () => {
    evaluatePhases.mockResolvedValue(['Running']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));

    // The workload goes away, which must evict it.
    useListMock.mockReturnValue([[], null]);
    rerender(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(onRows).toHaveBeenLastCalledWith('deployment', []));

    // It comes back unchanged: a surviving entry would be reused and skip the
    // call, so a second call proves the first was evicted.
    useListMock.mockReturnValue([[item('1')], null]);
    rerender(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(2));
  });

  // With allowed namespaces configured, useList runs a query per namespace and
  // hands back the ones that resolved while the rest are pending. Reporting
  // then would show a partial list as the whole of it.
  it('waits for every namespace query before reporting rows', async () => {
    evaluatePhases.mockResolvedValue(['Running']);
    useListMock.mockReturnValue(listResult([], null, true));
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    // Empty items with the query still loading must not report, or the table
    // settles on "no workloads" while a namespace is outstanding.
    expect(onRows).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();

    useListMock.mockReturnValue(listResult([item('1')], null, false));
    rerender(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(onRows).toHaveBeenCalledWith('deployment', expect.any(Array)));
  });

  it('surfaces the list error without calling evaluatePhases', () => {
    useListMock.mockReturnValue([null, { message: 'boom' }]);
    const onRows = vi.fn();
    const onError = vi.fn();

    render(<KindFetcher definition={definition()} cluster="cluster-a" plural="reactors" namespaced onRows={onRows} onError={onError} />);

    expect(onError).toHaveBeenCalledWith('deployment', new Error('boom'));
    expect(evaluatePhases).not.toHaveBeenCalled();
  });
});
