// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, sep } from 'node:path';
import * as ts from 'typescript';
import { afterEach, describe, expect, it } from 'vitest';

// Every module a source imports or re-exports from, found by parsing it, so
// that a comment or a string that looks like an import is not one, and an
// import with a comment inside it still is.
function moduleSpecifiers(file: string, source: string): string[] {
  const found: string[] = [];
  const visit = (node: ts.Node) => {
    if (
      (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) &&
      node.moduleSpecifier &&
      ts.isStringLiteral(node.moduleSpecifier)
    ) {
      found.push(node.moduleSpecifier.text);
    } else if (
      ts.isImportEqualsDeclaration(node) &&
      ts.isExternalModuleReference(node.moduleReference) &&
      ts.isStringLiteral(node.moduleReference.expression)
    ) {
      found.push(node.moduleReference.expression.text);
    } else if (
      ts.isImportTypeNode(node) &&
      ts.isLiteralTypeNode(node.argument) &&
      ts.isStringLiteral(node.argument.literal)
    ) {
      found.push(node.argument.literal.text);
    } else if (
      ts.isCallExpression(node) &&
      (node.expression.kind === ts.SyntaxKind.ImportKeyword ||
        (ts.isIdentifier(node.expression) && node.expression.text === 'require')) &&
      node.arguments[0] &&
      ts.isStringLiteralLike(node.arguments[0])
    ) {
      found.push(node.arguments[0].text);
    }
    ts.forEachChild(node, visit);
  };
  const kind = file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  visit(ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, kind));
  return found;
}

// The sources under libDir, in any subfolder, that import something inside
// componentsDir. Relative paths are resolved, so a nested file is judged by
// where its import lands and not by how many levels it climbs.
function importsOfComponents(libDir: string, componentsDir: string): string[] {
  return (readdirSync(libDir, { recursive: true }) as string[])
    .filter(name => /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name))
    .filter(name => {
      const file = join(libDir, name);
      return moduleSpecifiers(file, readFileSync(file, 'utf8')).some(
        specifier =>
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

  it('finds an import with a comment inside it, which a pattern on the text misses', () => {
    const { lib, components } = project({
      'lib/karta/lazy.ts': "const load = () => import(/* lazy */ '../../components/Cell');\n",
    });
    expect(importsOfComponents(lib, components)).toEqual(['lazy.ts']);
  });

  it('finds every kind of import and re-export', () => {
    const { lib, components } = project({
      'lib/karta/star.ts': "export * from '../../components/Cell';\n",
      'lib/karta/type.ts': "import type { Cell } from '../../components/Cell';\n",
      'lib/karta/equals.ts': "import cell = require('../../components/Cell');\n",
      'lib/karta/call.ts': "const cell = require('../../components/Cell');\n",
      'lib/karta/typeof.ts': "export type Cell = typeof import('../../components/Cell');\n",
      'lib/karta/jsx.tsx':
        "import { Cell } from '../../components/Cell';\nexport const a = <Cell />;\n",
    });
    expect(importsOfComponents(lib, components).sort()).toEqual([
      'call.ts',
      'equals.ts',
      'jsx.tsx',
      'star.ts',
      'type.ts',
      'typeof.ts',
    ]);
  });

  it('does not take a comment or a string for an import', () => {
    const { lib, components } = project({
      'lib/karta/line.ts': "// import '../../components/Cell';\nexport const a = 1;\n",
      'lib/karta/block.ts':
        "/* import { Cell } from '../../components/Cell'; */\nexport const a = 1;\n",
      'lib/karta/text.ts': 'export const note = "import \'../../components/Cell\'";\n',
    });
    expect(importsOfComponents(lib, components)).toEqual([]);
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
