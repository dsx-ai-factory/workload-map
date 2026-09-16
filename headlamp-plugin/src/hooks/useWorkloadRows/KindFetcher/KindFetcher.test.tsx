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

function definition(): Definition {
  return {
    karta: {
      apiVersion: 'run.ai/v1alpha1',
      kind: 'Karta',
      metadata: { name: 'deployment' },
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

describe('KindFetcher', () => {
  beforeEach(() => {
    evaluatePhases.mockReset();
    useListMock.mockReset();
  });

  it('computes status once per workload and reuses it when resourceVersion is unchanged', async () => {
    evaluatePhases.mockResolvedValue(['Running']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Running'] })])
    );

    // Same resourceVersion, new items array reference (a poll tick) — must
    // not recompute status.
    useListMock.mockReturnValue([[item('1')], null]);
    rerender(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);

    await waitFor(() => expect(onRows).toHaveBeenLastCalledWith('deployment', expect.any(Array)));
    expect(evaluatePhases).toHaveBeenCalledTimes(1);
  });

  it('recomputes status when resourceVersion changes', async () => {
    evaluatePhases.mockResolvedValueOnce(['Running']).mockResolvedValueOnce(['Failed']);
    useListMock.mockReturnValue([[item('1')], null]);
    const onRows = vi.fn();
    const onError = vi.fn();

    const { rerender } = render(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));

    useListMock.mockReturnValue([[item('2')], null]);
    rerender(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);

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

    const { rerender } = render(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(1));

    // A newer resourceVersion supersedes the still-pending v1 call before it
    // resolves.
    useListMock.mockReturnValue([[item('2')], null]);
    rerender(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() => expect(evaluatePhases).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Initializing'] })])
    );

    // The stale v1 call finally resolves — must not overwrite the cache
    // with ["Running"] now that v2's ["Initializing"] is current.
    resolveV1(['Running']);
    await v1Promise;

    useListMock.mockReturnValue([[item('2')], null]);
    rerender(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);
    await waitFor(() =>
      expect(onRows).toHaveBeenLastCalledWith('deployment', [expect.objectContaining({ phases: ['Initializing'] })])
    );
  });

  it('surfaces the list error without calling evaluatePhases', () => {
    useListMock.mockReturnValue([null, { message: 'boom' }]);
    const onRows = vi.fn();
    const onError = vi.fn();

    render(<KindFetcher definition={definition()} plural="reactors" namespaced onRows={onRows} onError={onError} />);

    expect(onError).toHaveBeenCalledWith('deployment', new Error('boom'));
    expect(evaluatePhases).not.toHaveBeenCalled();
  });
});
