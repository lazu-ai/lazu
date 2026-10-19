// Build-time parts of web extensions. A build with extensions has
// src/extensions/<name>/build.mjs, which may export:
//   chromeSections   { <name>: '<section path>' } — extra public chrome partials
//   llmsSections(env) markdown appended to llms.txt before the docs section
//   sitemaps(env)     extra sitemap URLs for the sitemap index
//   checkPrerender({ requireFile, locale, prefix }) — prerender output checks
//   devApiPaths       dev-server proxy patterns for its API-rendered pages
import { existsSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export async function loadBuildExtensions(appRoot) {
  const dir = path.join(appRoot, 'src', 'extensions');
  if (!existsSync(dir)) return [];
  const files = readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => path.join(dir, entry.name, 'build.mjs'))
    .filter((file) => existsSync(file))
    .sort();
  return Promise.all(files.map((file) => import(pathToFileURL(file).href)));
}
