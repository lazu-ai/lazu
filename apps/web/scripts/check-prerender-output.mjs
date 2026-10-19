#!/usr/bin/env node

import { readFileSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';

import { loadBuildExtensions } from './extensions.mjs';
import { allPublicRoutes } from './marketing-utils.mjs';

const buildExtensions = await loadBuildExtensions(process.cwd());

const fail = (message) => {
  console.error(`[prerender] ${message}`);
  process.exit(1);
};

// Every (page, locale) variant must carry its prerender root, its language
// and its own canonical.
for (const route of allPublicRoutes()) {
  const file =
    route.path === '/'
      ? path.join(process.cwd(), 'dist', 'index.html')
      : path.join(
          process.cwd(),
          'dist',
          route.path.replace(/^\/+/, '').replace(/\/+$/, ''),
          'index.html',
        );
  let routeHtml;
  try {
    routeHtml = readFileSync(file, 'utf8');
  } catch {
    fail(`${route.path} was not prerendered (${file} missing)`);
  }
  if (!routeHtml.includes(`data-prerender-route="${route.path}"`)) {
    fail(`${route.path} is missing its public prerender root`);
  }
  if (!routeHtml.includes(`<html lang="${route.locale}"`)) {
    fail(`${route.path} does not declare lang="${route.locale}"`);
  }
  if (!routeHtml.includes('hreflang="x-default"')) {
    fail(`${route.path} is missing hreflang alternates`);
  }
}

// The SPA shell, the 404 page and the chrome partials the API-rendered pages
// splice in must all exist; nginx serves each of them by name.
const requireFile = (relative, mustInclude = []) => {
  let content;
  try {
    content = readFileSync(path.join(process.cwd(), 'dist', relative), 'utf8');
  } catch {
    fail(`dist/${relative} is missing`);
  }
  for (const needle of mustInclude) {
    if (!content.includes(needle)) fail(`dist/${relative} lacks ${needle}`);
  }
};
requireFile('app.html', ['noindex', '<div id="root"></div>']);
requireFile('404.html', ['noindex', 'data-prerender-route="/__not-found"']);
requireFile('__chrome/head.html', ['rel="stylesheet"', 'type="module"']);
requireFile('__chrome/boot.html', ['lazu.theme']);
for (const locale of ['en', 'zh', 'zh-TW', 'ja']) {
  for (const section of ['models']) {
    requireFile(`__chrome/header.${locale}.${section}.html`, [
      'data-nav="models"',
      'aria-current="page"',
    ]);
    requireFile(`__chrome/footer.${locale}.${section}.html`, [
      'lazu-public-footer',
      'DUBRIO LLC',
    ]);
  }
  const prefix = locale === 'en' ? '' : `/${locale}`;
  for (const extension of buildExtensions) {
    extension.checkPrerender?.({ requireFile, locale, prefix });
  }
}

const htmlPath = path.join(process.cwd(), 'dist', 'index.html');
const html = readFileSync(htmlPath, 'utf8');

const inlineScripts = [
  ...html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g),
]
  .filter((match) => !/\bsrc=/.test(match[0]))
  .map((match) => match[1]);

const hydrationGuard = inlineScripts.find(
  (script) =>
    script.includes("const root = document.getElementById('root')") &&
    script.includes("root.dataset.hydrate = 'true'"),
);

if (!hydrationGuard) {
  fail('dist/index.html is missing the hydration guard');
}

try {
  // Compile without executing. This catches malformed regular expressions and
  // any other syntax damage introduced while injecting the guard into HTML.
  Function(hydrationGuard);
} catch (error) {
  fail(`hydration guard is not valid JavaScript: ${error.message}`);
}

if (hydrationGuard.includes('<div id="root"')) {
  fail('hydration guard contains injected root markup');
}

console.log('[prerender] output guard is valid');
