// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { Envelope, getKartaWasm } from './karta';
import { Karta, Workload, WorkloadTree } from './karta.types';

function unwrap<T>(envelope: Envelope, fallback: T): T {
  if (envelope.error !== null) {
    throw new Error(envelope.error);
  }
  if (envelope.data === null) {
    return fallback;
  }
  return JSON.parse(envelope.data) as T;
}

export async function buildTree(definition: Karta, workload: Workload): Promise<WorkloadTree> {
  const karta = await getKartaWasm();
  return unwrap(karta.buildTree(JSON.stringify(definition), JSON.stringify(workload)), {
    Status: null,
    Children: [],
  });
}

// evaluatePhases reads the phases off the tree today. It stays its own
// function so a caller asking only for status does not depend on how the
// status is reached, and a cheaper path replaces only this body.
export async function evaluatePhases(definition: Karta, workload: Workload): Promise<string[]> {
  const tree = await buildTree(definition, workload);
  return tree.Status?.Phases ?? [];
}

export async function listCatalog(): Promise<Karta[]> {
  const karta = await getKartaWasm();
  return unwrap(karta.listCatalog(), []);
}
