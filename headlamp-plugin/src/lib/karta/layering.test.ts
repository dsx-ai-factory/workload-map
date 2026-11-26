// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

import {
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
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

// The compiler options of the plugin itself, so an import is resolved the way
// the build resolves it, including a path that is not relative.
function pluginCompilerOptions(): ts.CompilerOptions {
  const configPath = ts.findConfigFile(__dirname, ts.sys.fileExists, 'tsconfig.json')!;
  const { config } = ts.readConfigFile(configPath, ts.sys.readFile);
  return ts.parseJsonConfigFileContent(config, ts.sys, dirname(configPath)).options;
}

// The sources under libDir, in any subfolder, whose imports resolve to a file
// inside componentsDir. Each import is resolved with the compiler options
// given, so a nested file is judged by where its import lands, and a path
// such as src/components/..., which does not start with a dot, is not missed.
function importsOfComponents(
  libDir: string,
  componentsDir: string,
  options: ts.CompilerOptions
): string[] {
  return (readdirSync(libDir, { recursive: true }) as string[])
    .filter(name => /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name))
    .filter(name => {
      const file = join(libDir, name);
      return moduleSpecifiers(file, readFileSync(file, 'utf8')).some(specifier => {
        const resolved = ts.resolveModuleName(specifier, file, options, ts.sys).resolvedModule;
        return (
          !!resolved && realpathSync(resolved.resolvedFileName).startsWith(componentsDir + sep)
        );
      });
    });
}

// lib is used by code that is not UI as well, so it must not reach up into
// components: the dependency would run the wrong way and could become a cycle.
describe('lib/karta', () => {
  it('does not import from components', () => {
    const dir = __dirname;
    expect(readdirSync(dir).filter(name => /\.tsx?$/.test(name)).length).toBeGreaterThan(0);

    const componentsDir = realpathSync(resolve(dir, '../../components'));
    expect(importsOfComponents(dir, componentsDir, pluginCompilerOptions())).toEqual([]);
  });

  // The guard is only as good as its resolution: a bare src/... path has to
  // reach the real component through the plugin's configuration.
  it('resolves a path that is not relative to the component it names', () => {
    const resolved = ts.resolveModuleName(
      'src/components/dataTable/cells/InfoTooltip',
      join(__dirname, 'statusItem.ts'),
      pluginCompilerOptions(),
      ts.sys
    ).resolvedModule;

    expect(resolved?.resolvedFileName).toBe(
      resolve(__dirname, '../../components/dataTable/cells/InfoTooltip.tsx')
    );
  });
});

describe('the layering check', () => {
  let root: string;
  afterEach(() => rmSync(root, { recursive: true, force: true }));

  // A small project laid out like the plugin: sources under src, and the
  // targets the fixtures import, which have to exist for an import to resolve.
  // Paths are relative to src, and baseUrl is the project root, as in the
  // plugin's configuration.
  const project = (files: Record<string, string>) => {
    root = realpathSync(mkdtempSync(join(tmpdir(), 'layering-')));
    const targets = [
      'components/Cell.tsx',
      'components/dataTable/cells/InfoTooltip.tsx',
      'lib/karta/direct.ts',
      'lib/karta/components/Local.ts',
      'other/Thing.ts',
    ];
    for (const name of targets) {
      files[name] ??= 'export const x = 1;\n';
    }
    for (const [name, content] of Object.entries(files)) {
      mkdirSync(dirname(join(root, 'src', name)), { recursive: true });
      writeFileSync(join(root, 'src', name), content);
    }
    const options: ts.CompilerOptions = { ...pluginCompilerOptions(), baseUrl: root, paths: {} };
    return {
      lib: join(root, 'src/lib/karta'),
      components: join(root, 'src/components'),
      options,
    };
  };
  const found = (files: Record<string, string>) => {
    const { lib, components, options } = project(files);
    return importsOfComponents(lib, components, options).sort();
  };

  it('finds a direct import of a component', () => {
    expect(
      found({
        'lib/karta/importer.ts': "import { Cell } from '../../components/Cell';\n",
        'lib/karta/clean.ts': "import { x } from './direct';\n",
      })
    ).toEqual(['importer.ts']);
  });

  // TypeScript resolves this through baseUrl to the component, and a check on
  // the text of the path, which looks for a leading dot, lets it through.
  it('finds an import by a path that does not start with a dot', () => {
    expect(
      found({
        'lib/karta/statusItem.ts':
          "export type { InfoTooltipProps } from 'src/components/dataTable/cells/InfoTooltip';\n",
      })
    ).toEqual(['statusItem.ts']);
  });

  it('finds an import in a nested folder, which climbs one level more', () => {
    expect(
      found({
        'lib/karta/nested/deeper/inner.ts': "export { Cell } from '../../../../components/Cell';\n",
        'lib/karta/nested/fine.ts': "import { x } from '../direct';\n",
      })
    ).toEqual([join('nested', 'deeper', 'inner.ts')]);
  });

  it('finds an import with a comment inside it, which a pattern on the text misses', () => {
    expect(
      found({
        'lib/karta/lazy.ts': "const load = () => import(/* lazy */ '../../components/Cell');\n",
      })
    ).toEqual(['lazy.ts']);
  });

  it('finds every kind of import and re-export', () => {
    expect(
      found({
        'lib/karta/star.ts': "export * from '../../components/Cell';\n",
        'lib/karta/type.ts': "import type { Cell } from '../../components/Cell';\n",
        'lib/karta/equals.ts': "import cell = require('../../components/Cell');\n",
        'lib/karta/call.ts': "const cell = require('../../components/Cell');\n",
        'lib/karta/typeof.ts': "export type Cell = typeof import('../../components/Cell');\n",
        'lib/karta/jsx.tsx':
          "import { Cell } from '../../components/Cell';\nexport const a = <Cell />;\n",
      })
    ).toEqual(['call.ts', 'equals.ts', 'jsx.tsx', 'star.ts', 'type.ts', 'typeof.ts']);
  });

  it('does not take a comment or a string for an import', () => {
    expect(
      found({
        'lib/karta/line.ts': "// import '../../components/Cell';\nexport const a = 1;\n",
        'lib/karta/block.ts':
          "/* import { Cell } from '../../components/Cell'; */\nexport const a = 1;\n",
        'lib/karta/text.ts': 'export const note = "import \'../../components/Cell\'";\n',
      })
    ).toEqual([]);
  });

  it('finds a dynamic import, and leaves test files and look-alike folders alone', () => {
    expect(
      found({
        'lib/karta/lazy.ts': "const load = () => import('../../components/Cell');\n",
        'lib/karta/lazy.test.ts': "import { Cell } from '../../components/Cell';\n",
        'lib/karta/nested/elsewhere.ts': "import { x } from '../../other/Thing';\n",
        // Resolves to lib/karta/components, a folder of its own.
        'lib/karta/nested/local.ts': "import { x } from '../components/Local';\n",
        // A bare path to somewhere else is not a component either.
        'lib/karta/nested/other.ts': "import { x } from 'src/other/Thing';\n",
      })
    ).toEqual(['lazy.ts']);
  });
});
