#!/usr/bin/env node

import { readFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import path from 'node:path';
import process from 'node:process';

const root = process.cwd();
const dist = path.join(root, 'dist');
const manifestPath = path.join(dist, '.vite', 'manifest.json');
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));

const budgets = {
  // 2026-09: the entry stopped carrying two libraries it could not use on the
  // page that downloads it. react-day-picker and date-fns now load with the
  // calendar popover, and the Markdown pipeline with the legal document that
  // needs it, taking the first download from 339.61 to 264.73 KiB gzip on CI.
  // The ceiling drops with it — at 350 the win would have leaked back out one
  // import at a time — and keeps ~15 KiB of headroom.
  initialJavaScript: 280 * 1024,
  initialCss: 65 * 1024,
  // The Markdown graph absorbed what the entry gave up: micromark, remark and
  // the mdast utilities are ~47 KiB gzip and used to be paid by every visitor
  // to every page, including the landing page. They are now paid once, by the
  // reader who opens something written in Markdown — 137.03 → 184.11 KiB — and
  // that reader is still 28 KiB better off than before. The ceiling moves to
  // cover the relocation and no more.
  markdownJavaScript: 190 * 1024,
  markdownCss: 12 * 1024,
  vchartRuntime: 350 * 1024,
  // Whole locale graphs, including errors and every fallback language. The
  // blocking cold-start budgets below also include the application entry.
  locale: {
    default: 76 * 1024,
    zh: 130 * 1024,
    'zh-TW': 170 * 1024,
    ja: 135 * 1024,
  },
  consoleRouteJavaScript: 175 * 1024,
  marketingRouteJavaScript: 20 * 1024,
  pricingRouteJavaScript: 125 * 1024,
};

const failures = [];
const rows = [];

const gzipBytes = (file) =>
  gzipSync(readFileSync(path.join(dist, file)), { level: 9 }).byteLength;

const addBudget = (name, files, limit, { blocking = true } = {}) => {
  const bytes = files.reduce((total, file) => total + gzipBytes(file), 0);
  rows.push({ name, bytes, limit, files, blocking });
  if (blocking && bytes > limit)
    failures.push(`${name}: ${bytes} > ${limit} bytes gzip`);
};

const entry = manifest['index.html'];
if (!entry?.isEntry)
  throw new Error('Vite manifest is missing index.html entry');

const staticKeys = new Set();
const collectStaticImports = (key) => {
  if (staticKeys.has(key)) return;
  staticKeys.add(key);
  for (const dependency of manifest[key]?.imports ?? []) {
    collectStaticImports(dependency);
  }
};
collectStaticImports('index.html');

const initialJavaScript = [...staticKeys]
  .map((key) => manifest[key]?.file)
  .filter((file) => typeof file === 'string' && file.endsWith('.js'));
const initialCss = [...staticKeys].flatMap((key) => manifest[key]?.css ?? []);
addBudget(
  'Initial JavaScript',
  [...new Set(initialJavaScript)],
  budgets.initialJavaScript,
);
addBudget('Initial CSS', [...new Set(initialCss)], budgets.initialCss);

const findEntryKey = (predicate, label) => {
  const entryKey = Object.entries(manifest).find(([, item]) =>
    predicate(item),
  )?.[0];
  if (!entryKey) throw new Error(`Vite manifest is missing ${label}`);
  return entryKey;
};

const collectNonStaticGraph = (entryKey) => {
  const graph = new Set();
  const visit = (key) => {
    if (staticKeys.has(key) || graph.has(key)) return;
    const item = manifest[key];
    if (!item) throw new Error(`Vite manifest is missing ${key}`);
    graph.add(key);
    for (const dependency of item.imports ?? []) visit(dependency);
  };
  visit(entryKey);
  return graph;
};

const markdownKey = findEntryKey(
  (item) => item.name === 'MarkdownRenderer' && item.isDynamicEntry,
  'MarkdownRenderer dynamic entry',
);
const markdown = manifest[markdownKey];
const markdownKeys = collectNonStaticGraph(markdownKey);
addBudget(
  'Markdown renderer JavaScript',
  [...markdownKeys]
    .map((key) => manifest[key]?.file)
    .filter((file) => typeof file === 'string' && file.endsWith('.js')),
  budgets.markdownJavaScript,
);
const markdownCss = [
  ...new Set([...markdownKeys].flatMap((key) => manifest[key]?.css ?? [])),
];
addBudget('Markdown renderer CSS', markdownCss, budgets.markdownCss);

const addEntryGraphBudget = (name, entryKey, limit) => {
  const graph = collectNonStaticGraph(entryKey);
  const files = [...graph]
    .map((key) => manifest[key]?.file)
    .filter((file) => typeof file === 'string' && file.endsWith('.js'));
  addBudget(name, [...new Set(files)], limit);
};

// ConsoleRoot and MarketingLayout are now statically imported by AppRoot/App,
// so their shell payload is already covered by the Initial JavaScript budget.
// Keep route-level budgets on the current lazy equivalents to catch growth in
// the heaviest authenticated route and the public marketing locale chunks.
// That authenticated route is now 模型运营, which absorbed 模型管理, 定价运营 and
// 渠道; the lookup throws rather than warns when the source path goes stale, so
// it has to move with the page.
addEntryGraphBudget(
  'Console model ops route graph',
  findEntryKey(
    (item) =>
      item.src === 'src/pages/ModelOps/index.tsx' && item.isDynamicEntry,
    'Console model ops route entry',
  ),
  budgets.consoleRouteJavaScript,
);

for (const locale of ['en', 'zh', 'zh-TW', 'ja']) {
  addEntryGraphBudget(
    `Marketing ${locale} route graph`,
    findEntryKey(
      (item) =>
        item.src === `virtual:lazu-locale/marketing/${locale}` &&
        item.isDynamicEntry,
      `Marketing ${locale} route entry`,
    ),
    budgets.marketingRouteJavaScript,
  );
}

const vchartEntryNames = new Set(['VChartSimple', 'vchart-simple']);
const vchartEntryKeys = Object.entries(manifest)
  .filter(([, item]) => item?.isDynamicEntry && vchartEntryNames.has(item.name))
  .map(([key]) => key);
if (vchartEntryKeys.length !== vchartEntryNames.size) {
  throw new Error('Vite manifest is missing one or more lazy VChart entries');
}
const vchartKeys = new Set();
for (const key of vchartEntryKeys) {
  for (const dependency of collectNonStaticGraph(key))
    vchartKeys.add(dependency);
}
const vchartJavaScript = [...vchartKeys]
  .map((key) => manifest[key]?.file)
  .filter((file) => typeof file === 'string' && file.endsWith('.js'));
addBudget(
  'VChart runtime',
  [...new Set(vchartJavaScript)],
  budgets.vchartRuntime,
);

for (const locale of ['en', 'zh', 'zh-TW', 'ja']) {
  const localeKeys = [
    `virtual:lazu-locale/translation/${locale}`,
    `virtual:lazu-locale/marketing/${locale}`,
    `virtual:lazu-locale/errors/${locale}`,
  ];
  const localeGraph = new Set();
  // `errors` loads after the first screen (i18n loadErrorsNamespace), so it
  // counts toward the locale budget but not the cold startup one.
  const startupLocaleGraph = new Set();
  for (const key of localeKeys) {
    const localeEntry = manifest[key];
    if (!localeEntry?.isDynamicEntry) {
      throw new Error(`Vite manifest is missing the lazy ${key} locale`);
    }
    for (const dependency of collectNonStaticGraph(key)) {
      localeGraph.add(dependency);
      if (!key.startsWith('virtual:lazu-locale/errors/')) {
        startupLocaleGraph.add(dependency);
      }
    }
  }
  addBudget(
    `Locale ${locale}`,
    [...localeGraph]
      .map((key) => manifest[key]?.file)
      .filter((file) => typeof file === 'string' && file.endsWith('.js')),
    budgets.locale[locale] ?? budgets.locale.default,
    { blocking: false },
  );
  // Include namespaces loaded during startup, not just static imports. This
  // guards the actual cold marketing download for each fallback chain.
  addBudget(
    `Cold startup ${locale}`,
    [
      ...new Set([
        ...initialJavaScript,
        ...[...startupLocaleGraph]
          .map((key) => manifest[key]?.file)
          .filter((file) => typeof file === 'string' && file.endsWith('.js')),
      ]),
    ],
    { en: 375, zh: 425, 'zh-TW': 460, ja: 440 }[locale] * 1024,
  );
}

const html = readFileSync(path.join(dist, 'index.html'), 'utf8');
const preloadedFiles = [
  ...html.matchAll(/rel="modulepreload"[^>]+href="\/([^"?]+)"/g),
].map((match) => match[1]);
const allowedPreloads = new Set(initialJavaScript.slice(1));
for (const file of preloadedFiles) {
  if (!allowedPreloads.has(file)) {
    failures.push(`index.html eagerly preloads non-static chunk: ${file}`);
  }
}

for (const row of rows) {
  const size = (row.bytes / 1024).toFixed(2);
  const limit = (row.limit / 1024).toFixed(0);
  const status =
    row.bytes <= row.limit ? 'PASS' : row.blocking ? 'FAIL' : 'OVER';
  console.log(
    `${status.padEnd(4)} ${row.name}: ${size} KiB gzip / ${limit} KiB`,
  );
}

if (failures.length > 0) {
  console.error('\nBundle budget violations:');
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exit(1);
}
