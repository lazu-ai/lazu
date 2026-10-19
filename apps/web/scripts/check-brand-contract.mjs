import { readdir, readFile } from 'node:fs/promises';
import { extname, join, relative, resolve } from 'node:path';
import process from 'node:process';

const root = resolve(import.meta.dirname, '..');
const sourceRoot = join(root, 'src');
const checkedExtensions = new Set(['.css', '.ts', '.tsx']);
const forbidden = [
  {
    pattern: /@radix-ui\/|from\s+['"]radix-ui['"]/g,
    reason: 'Radix is not an accepted primitive foundation for apps/web',
  },
  {
    pattern: /\basChild\b/g,
    reason: 'use the Base UI render composition contract',
  },
  {
    pattern: /--radix-|data-radix-/g,
    reason: 'Radix CSS and DOM coupling is not part of the Lazu UI contract',
  },
  {
    pattern: /ui-v2/g,
    reason:
      'the versioned UI namespace is retired; use Lazu UI from components/ui',
  },
  {
    pattern: /--semi-[\w-]+/g,
    reason: 'Semi CSS variables are not part of the Brand contract',
  },
  {
    pattern: /(?:Inter Tight|JetBrains Mono)/g,
    reason: 'use --lazu-font-sans or --lazu-font-mono',
    // fonts.css registers the assets; brand.generated.css is generated from
    // canonical Brand tokens that select the landing-specific font families.
    allowedFiles: new Set([
      'src/styles/brand.generated.css',
      'src/styles/fonts.css',
    ]),
  },
];

async function collectFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map(async (entry) => {
      const path = join(directory, entry.name);
      return entry.isDirectory() ? collectFiles(path) : [path];
    }),
  );
  return files.flat().filter((file) => checkedExtensions.has(extname(file)));
}

const violations = [];
const files = [...(await collectFiles(sourceRoot)), join(root, 'package.json')];
for (const file of files) {
  const source = await readFile(file, 'utf8');
  // path.relative() follows the host platform, while the allow-list is stored
  // with repository-style POSIX separators. Normalize before matching so the
  // Brand contract behaves identically on Windows, macOS, and Linux.
  const relativeFile = relative(root, file).replaceAll('\\', '/');
  if (
    relativeFile.startsWith('src/') &&
    !relativeFile.startsWith('src/components/ui/') &&
    source.includes('@base-ui/react')
  ) {
    const line = source
      .slice(0, source.indexOf('@base-ui/react'))
      .split('\n').length;
    violations.push(
      `${relativeFile}:${line}: @base-ui/react (Base UI imports belong in components/ui)`,
    );
  }
  for (const rule of forbidden) {
    if (rule.allowedFiles?.has(relativeFile)) continue;
    for (const match of source.matchAll(rule.pattern)) {
      const line = source.slice(0, match.index).split('\n').length;
      violations.push(`${relativeFile}:${line}: ${match[0]} (${rule.reason})`);
    }
  }
}

if (violations.length > 0) {
  console.error('Brand contract violations:\n' + violations.join('\n'));
  process.exit(1);
}

console.log('Brand contract check passed.');
