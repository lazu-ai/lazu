import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

type AstNode = {
  type: string;
  value?: string;
  nodes?: AstNode[];
  parent?: AstNode;
};
type BracesOptions = Record<string, unknown>;
type BracesApi = {
  (input: string, options?: BracesOptions): string[];
  parse(input: string, options?: BracesOptions): AstNode;
  compile(input: string | AstNode, options?: BracesOptions): string;
  expand(input: string | AstNode, options?: BracesOptions): string[];
  stringify(input: string | AstNode, options?: BracesOptions): string;
};

const require = createRequire(import.meta.url);
const braces = require('../../vendor/braces') as BracesApi;
const malicious = '{'.repeat(4000) + 'a,b' + '}'.repeat(4000);

function expectDepthRejection(
  operation: () => unknown,
  code = 'ERR_BRACES_NESTING_DEPTH',
) {
  expect(operation).toThrowError(SyntaxError);
  try {
    operation();
  } catch (error) {
    expect(error).toMatchObject({ code });
    expect(String(error)).not.toContain('Maximum call stack');
    return;
  }
  throw new Error('Unsafe nested pattern was accepted');
}

describe('private braces fork blocks recursive stack exhaustion', () => {
  it('reverses its recorded patch to the exact original published file hashes', () => {
    const vendor = resolve('vendor/braces');
    const output = resolve('../../.local-results/test-results');
    mkdirSync(output, { recursive: true });
    const temporary = mkdtempSync(join(output, 'braces-provenance-'));
    try {
      cpSync(vendor, temporary, { recursive: true });
      execFileSync(
        'git',
        [
          'apply',
          '--reverse',
          '--unidiff-zero',
          '--whitespace=nowarn',
          join(vendor, 'PATCH.diff'),
        ],
        { cwd: temporary },
      );
      const upstream = JSON.parse(
        readFileSync(join(vendor, 'UPSTREAM.json'), 'utf8'),
      ) as {
        integrity: string;
        filesSha256: Record<string, string>;
      };
      const metadata = require('../../vendor/braces/package.json') as {
        lazuUpstream: { integrity: string };
      };
      expect(metadata.lazuUpstream.integrity).toBe(upstream.integrity);
      for (const [file, digest] of Object.entries(upstream.filesSha256)) {
        expect(
          createHash('sha256')
            .update(readFileSync(join(temporary, file)))
            .digest('hex'),
        ).toBe(digest);
      }
    } finally {
      rmSync(temporary, { recursive: true, force: true });
    }
  });

  it.each(['parse', 'compile', 'expand', 'stringify'] as const)(
    '%s rejects the advisory payload below the existing character cap',
    (method) => {
      expect(malicious.length).toBeLessThan(10000);
      expectDepthRejection(() => braces[method](malicious));
    },
  );

  it('default and expanded entry points reject unbalanced and mixed nesting', () => {
    expectDepthRejection(() => braces(malicious));
    expectDepthRejection(() => braces(malicious, { expand: true }));
    expectDepthRejection(() => braces.compile('{'.repeat(4000)));
    const mixed = '{('.repeat(2000) + 'x,y' + ')}'.repeat(2000);
    expectDepthRejection(() => braces.expand(mixed));
    expectDepthRejection(() =>
      braces.compile('('.repeat(4000) + 'x' + ')'.repeat(4000)),
    );
  });

  it.each(['compile', 'expand', 'stringify'] as const)(
    '%s also rejects an externally supplied deep or cyclic AST',
    (method) => {
      let ast: AstNode = { type: 'text', value: 'x' };
      for (let depth = 0; depth < 4000; depth++) {
        ast = { type: 'paren', nodes: [ast] };
      }
      expectDepthRejection(() => braces[method](ast));
      const cycle: AstNode = { type: 'root', nodes: [] };
      cycle.nodes = [cycle];
      expectDepthRejection(() => braces[method](cycle), 'ERR_BRACES_AST_CYCLE');
    },
  );

  it('caller options cannot raise the safety bound', () => {
    expectDepthRejection(() =>
      braces.compile(malicious, { maxDepth: Infinity }),
    );
  });

  it('bounds separate parent chains without rejecting valid parser back-links', () => {
    const child: AstNode = { type: 'paren', nodes: [] };
    child.parent = child;
    const root: AstNode = { type: 'root', nodes: [child] };
    expectDepthRejection(() => braces.expand(root), 'ERR_BRACES_AST_CYCLE');
    const second: AstNode = { type: 'paren', parent: child };
    child.parent = second;
    expectDepthRejection(() => braces.expand(root), 'ERR_BRACES_AST_CYCLE');
    let parent: AstNode = { type: 'root' };
    for (let depth = 0; depth < 200; depth++) {
      parent = { type: 'paren', parent };
    }
    child.parent = parent;
    expectDepthRejection(() => braces.expand(root));
    expect(braces.expand(braces.parse('src/{a,{b,c}}.ts'))).toEqual([
      'src/a.ts',
      'src/b.ts',
      'src/c.ts',
    ]);
  });

  it('rejects wide malformed ASTs within a finite node budget', () => {
    const wide: AstNode = {
      type: 'root',
      nodes: Array.from({ length: 20004 }, () => ({
        type: 'text',
        value: 'x',
      })),
    };
    expectDepthRejection(() => braces.compile(wide), 'ERR_BRACES_AST_SIZE');
  });

  it('retains ordinary glob/range, quoted, escaped and parsed-AST behavior', () => {
    expect(braces.expand('src/{api,ui}/file-{1..3}.ts')).toEqual([
      'src/api/file-1.ts',
      'src/api/file-2.ts',
      'src/api/file-3.ts',
      'src/ui/file-1.ts',
      'src/ui/file-2.ts',
      'src/ui/file-3.ts',
    ]);
    expect(braces.compile('{src,test}/**/*.{ts,tsx}')).toBe(
      '(src|test)/**/*.(ts|tsx)',
    );
    expect(braces.expand('src/\\{literal\\}.ts')).toEqual(['src/{literal}.ts']);
    expect(braces.expand('src/"{a,b}".ts')).toEqual(['src/{a,b}.ts']);
    const nested = '{'.repeat(50) + 'a,b' + '}'.repeat(50);
    expect(braces.stringify(braces.parse(nested))).toBe(nested);
    expect(braces.compile(braces.parse('src/{a,b}.ts'))).toBe('src/(a|b).ts');
    expect(braces.expand(braces.parse('src/{a,b}.ts'))).toEqual([
      'src/a.ts',
      'src/b.ts',
    ]);
  });

  it('installed micromatch and Tailwind chokidar resolve the hardened private source', async () => {
    const micromatchRequire = createRequire(require.resolve('micromatch'));
    const tailwindRequire = createRequire(require.resolve('tailwindcss'));
    const chokidarRequire = createRequire(tailwindRequire.resolve('chokidar'));
    for (const consumerRequire of [micromatchRequire, chokidarRequire]) {
      const installedRoot = dirname(
        consumerRequire.resolve('braces/package.json'),
      );
      expect(consumerRequire('braces/package.json')).toMatchObject({
        name: '@lazu/braces',
        private: true,
      });
      for (const file of [
        'LICENSE',
        'index.js',
        'lib/depth.js',
        'lib/constants.js',
        'lib/utils.js',
        'lib/parse.js',
        'lib/compile.js',
        'lib/expand.js',
        'lib/stringify.js',
      ]) {
        expect(readFileSync(join(installedRoot, file))).toEqual(
          readFileSync(resolve('vendor/braces', file)),
        );
      }
      const installed = consumerRequire('braces') as BracesApi;
      expectDepthRejection(() => installed.compile(malicious));
      expectDepthRejection(() => installed.expand(malicious));
    }
    const micromatch = require('micromatch') as {
      braces(pattern: string, options?: BracesOptions): string[];
    };
    expect(micromatch.braces('src/{a,b}.ts', { expand: true })).toEqual([
      'src/a.ts',
      'src/b.ts',
    ]);
    expectDepthRejection(() => micromatch.braces(malicious, { expand: true }));
    const chokidar = tailwindRequire('chokidar') as {
      FSWatcher: new (options: BracesOptions) => {
        _getWatchHelpers(pattern: string, depth: number): unknown;
        close(): Promise<void>;
      };
    };
    const watcher = new chokidar.FSWatcher({ persistent: false });
    try {
      expect(watcher._getWatchHelpers('src/{a,b}/**/*.ts', 0)).toBeDefined();
      expectDepthRejection(() =>
        watcher._getWatchHelpers('src/' + malicious + '/*.ts', 0),
      );
    } finally {
      await watcher.close();
    }
  });
});
