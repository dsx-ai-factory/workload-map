// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { describe, expect, it } from 'vitest';
import { buildWorkloadColumns, OPTIONAL_COLUMN_IDS } from './columns';

const DEFAULT_COLUMN_IDS = ['workload', 'type', 'namespace', 'status', 'pods', 'gpus', 'age'];

describe('buildWorkloadColumns', () => {
  it('omits the cluster column when includeCluster is false', () => {
    const columns = buildWorkloadColumns(false);

    expect(columns.map(c => c.id)).toEqual([...DEFAULT_COLUMN_IDS, ...OPTIONAL_COLUMN_IDS]);
  });

  it('includes the cluster column right after the default columns when includeCluster is true', () => {
    const columns = buildWorkloadColumns(true);

    expect(columns.map(c => c.id)).toEqual([...DEFAULT_COLUMN_IDS, 'cluster', ...OPTIONAL_COLUMN_IDS]);
  });

  it('every optional column id in OPTIONAL_COLUMN_IDS has a matching column', () => {
    const columns = buildWorkloadColumns(true);
    const ids = new Set(columns.map(c => c.id));

    for (const optionalId of OPTIONAL_COLUMN_IDS) {
      expect(ids.has(optionalId)).toBe(true);
    }
  });
});
