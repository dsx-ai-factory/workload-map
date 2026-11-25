// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { readdirSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// lib is used by code that is not UI as well, so it must not reach up into
// components: the dependency would run the wrong way and could become a cycle.
describe('lib/karta', () => {
  it('does not import from components', () => {
    const dir = `${__dirname}/`;
    const sources = readdirSync(dir).filter(
      name => /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)
    );
    expect(sources.length).toBeGreaterThan(0);

    const offenders = sources.filter(name =>
      /from\s+['"](\.\.\/)+components\//.test(readFileSync(dir + name, 'utf8'))
    );
    expect(offenders).toEqual([]);
  });
});
