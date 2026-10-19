import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const webDir = path.resolve(scriptDir, '..');
const repoDir = path.resolve(webDir, '../..');
const brandTokenDir = path.join(repoDir, 'brand', 'tokens');
const outputPath = path.join(webDir, 'src', 'styles', 'brand.generated.css');
const checkOnly = process.argv.includes('--check');

const sources = ['colors.css', 'data-viz.css', 'ui.css', 'public-web.css'];

const chunks = await Promise.all(
  sources.map(async (source) => {
    const contents = await readFile(path.join(brandTokenDir, source), 'utf8');
    return `/* source: brand/tokens/${source} */\n${contents.trim()}\n`;
  }),
);

const generated = `/*
 * GENERATED FILE — DO NOT EDIT.
 * Canonical source: brand/tokens/*.css
 * Regenerate: bun run brand:sync
 * Verify drift: bun run brand:check
 */

${chunks.join('\n')}`;

if (checkOnly) {
  let current = '';
  try {
    current = await readFile(outputPath, 'utf8');
  } catch {
    // A missing generated file is reported as drift below.
  }

  if (current !== generated) {
    console.error(
      'Brand token drift detected. Run `cd apps/web && bun run brand:sync`.',
    );
    process.exitCode = 1;
  } else {
    console.log('Brand token check passed.');
  }
} else {
  await writeFile(outputPath, generated);
  console.log(
    `Synced ${sources.length} Brand token files to ${path.relative(repoDir, outputPath)}.`,
  );
}
