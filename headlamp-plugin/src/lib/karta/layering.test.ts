// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

const IMPORT_SPECIFIER = /(?:from|import)\s*\(?\s*['"]([^'"]+)['"]/g;

// The sources under libDir, in any subfolder, that import something inside
// componentsDir. Relative paths are resolved, so a nested file is judged by
// where its import lands and not by how many levels it climbs.
function importsOfComponents(libDir: string, componentsDir: string): string[] {
  return (readdirSync(libDir, { recursive: true }) as string[])
    .filter(name => /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name))
    .filter(name => {
      const file = join(libDir, name);
      return [...readFileSync(file, 'utf8').matchAll(IMPORT_SPECIFIER)].some(
        ([, specifier]) =>
          specifier.startsWith('.') &&
          resolve(dirname(file), specifier).startsWith(componentsDir + sep)
      );
    });
}

// lib is used by code that is not UI as well, so it must not reach up into
// components: the dependency would run the wrong way and could become a cycle.
describe('lib/karta', () => {
  it('does not import from components', () => {
    const dir = __dirname;
    expect(readdirSync(dir).filter(name => /\.tsx?$/.test(name)).length).toBeGreaterThan(0);

    expect(importsOfComponents(dir, resolve(dir, '../../components'))).toEqual([]);
  });
});

describe('the layering check', () => {
  let root: string;
  afterEach(() => rmSync(root, { recursive: true, force: true }));

  const project = (files: Record<string, string>) => {
    root = mkdtempSync(join(tmpdir(), 'layering-'));
    for (const [name, content] of Object.entries(files)) {
      mkdirSync(dirname(join(root, name)), { recursive: true });
      writeFileSync(join(root, name), content);
    }
    return { lib: join(root, 'lib/karta'), components: join(root, 'components') };
  };

  it('finds a direct import of a component', () => {
    const { lib, components } = project({
      'lib/karta/direct.ts': "import { Cell } from '../../components/Cell';\n",
      'lib/karta/clean.ts': "import { other } from './direct';\n",
    });
    expect(importsOfComponents(lib, components)).toEqual(['direct.ts']);
  });

  it('finds an import in a nested folder, which climbs one level more', () => {
    const { lib, components } = project({
      'lib/karta/nested/deeper/inner.ts': "export { Cell } from '../../../../components/Cell';\n",
      'lib/karta/nested/fine.ts': "import { x } from '../direct';\n",
    });
    expect(importsOfComponents(lib, components)).toEqual([join('nested', 'deeper', 'inner.ts')]);
  });

  it('finds a dynamic import, and leaves test files and look-alike folders alone', () => {
    const { lib, components } = project({
      'lib/karta/lazy.ts': "const load = () => import('../../components/Cell');\n",
      'lib/karta/lazy.test.ts': "import { Cell } from '../../components/Cell';\n",
      'lib/karta/nested/elsewhere.ts': "import { x } from '../../other/Thing';\n",
      // Resolves to lib/karta/components, a folder of its own.
      'lib/karta/nested/local.ts': "import { x } from '../components/Local';\n",
    });
    expect(importsOfComponents(lib, components)).toEqual(['lazy.ts']);
  });
});
