// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Requires karta-wasm/karta.wasm + wasm_exec.js to already be built:
// `make karta-wasm` from the repo root (or `make headlamp-plugin-build`,
// which builds it before running the plugin's tests).

import * as crypto from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import * as perfHooks from 'node:perf_hooks';
import { beforeAll, describe, expect, it, vi } from 'vitest';

const { getKartaWasm } = vi.hoisted(() => ({ getKartaWasm: vi.fn() }));
vi.mock('./karta', () => ({ getKartaWasm }));

import type { Karta, Workload } from './karta.types';
import { buildTree, evaluatePhases, listCatalog } from './kartaUtil';

const KARTA_WASM_DIR = path.resolve(__dirname, '../../../../karta-wasm');
const WASM_PATH = path.join(KARTA_WASM_DIR, 'karta.wasm');
const WASM_EXEC_PATH = path.join(KARTA_WASM_DIR, 'wasm_exec.js');

beforeAll(async () => {
  if (!existsSync(WASM_PATH) || !existsSync(WASM_EXEC_PATH)) {
    throw new Error(
      `${WASM_PATH} not found — run "make karta-wasm" from the repo root before running this test.`,
    );
  }

  // wasm_exec.js is a browser-targeted script that assigns globalThis.Go; it
  // expects a couple of globals a plain Node process doesn't provide.
  const globals = globalThis as unknown as Record<string, unknown>;
  globals.crypto ??= crypto.webcrypto;
  globals.performance ??= perfHooks.performance;

  require(WASM_EXEC_PATH);

  const GoRuntime = globals.Go as new () => {
    importObject: WebAssembly.Imports;
    run(instance: WebAssembly.Instance): Promise<void>;
  };
  const go = new GoRuntime();
  const { instance } = await WebAssembly.instantiate(readFileSync(WASM_PATH), go.importObject);
  go.run(instance);

  getKartaWasm.mockResolvedValue(globals.karta);
}, 20_000);

function podFixture(phase: string): Workload {
  return {
    apiVersion: 'v1',
    kind: 'Pod',
    metadata: { name: 'contract-pod', namespace: 'default' },
    spec: { containers: [{ name: 'main', image: 'busybox' }] },
    status: { phase },
  };
}

describe('listCatalog', () => {
  it('returns the embedded catalog, including the built-in Pod definition', async () => {
    const catalog = await listCatalog();

    expect(catalog.length).toBeGreaterThan(0);
    expect(catalog.some(k => k.spec.structureDefinition.rootComponent.kind?.kind === 'Pod')).toBe(
      true,
    );
  });
});

describe('against the built-in Pod definition', () => {
  let podDefinition: Karta;

  beforeAll(async () => {
    const catalog = await listCatalog();
    const found = catalog.find(k => k.spec.structureDefinition.rootComponent.kind?.kind === 'Pod');
    if (!found) {
      throw new Error('expected the embedded catalog to contain the built-in Pod definition');
    }
    podDefinition = found;
  });

  describe('buildTree', () => {
    it('matches the Running phase (the Pod definition has no child components, so Children is empty)', async () => {
      const tree = await buildTree(podDefinition, podFixture('Running'));

      expect(tree.Status?.Phases).toEqual(['Running']);
      expect(tree.Children).toEqual([]);
    });

    it('rejects with the real Go error for an invalid definition', async () => {
      await expect(buildTree({} as Karta, podFixture('Running'))).rejects.toThrow();
    });
  });

  describe('evaluatePhases', () => {
    it('reads the phases off the tree', async () => {
      await expect(evaluatePhases(podDefinition, podFixture('Failed'))).resolves.toEqual([
        'Failed',
      ]);
    });

    it('rejects with the real Go error for an invalid definition', async () => {
      await expect(evaluatePhases({} as Karta, podFixture('Running'))).rejects.toThrow();
    });
  });
});
