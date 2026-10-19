// Shared helpers for the build-time SEO/prerender scripts. The public site
// includes the marketing and legal-document routes; English lives unprefixed
// and zh / zh-TW / ja get crawlable URL prefixes (/zh/, /zh/faq,
// /zh/privacy, …) — before those existed the translations only rendered
// client-side and no crawler ever saw them.

import path from 'node:path';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const appRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(appRoot, '../..');
const supportedMarketingLanguages = ['en', 'zh', 'zh-TW', 'ja'];

const normalizePublicLanguage = (language = 'en') => {
  const normalized = String(language).replace(/_/g, '-');
  if (/^zh[-]?(tw|hk|hant|mo)/i.test(normalized)) return 'zh-TW';
  if (/^zh[-]?(cn|hans)?$/i.test(normalized)) return 'zh';
  const short = normalized.split('-')[0].toLowerCase();
  return supportedMarketingLanguages.includes(short) ? short : 'en';
};

export const paths = {
  appRoot,
  repoRoot,
  publicDir: path.join(appRoot, 'public'),
  distDir: path.join(appRoot, 'dist'),
};

export const env = {
  siteOrigin: process.env.SITE_ORIGIN || 'https://lazu.ai',
  apiOrigin: process.env.API_ORIGIN || 'https://api.lazu.ai',
  // Docs are served under the main domain (lazu.ai/docs).
  docsOrigin: process.env.DOCS_ORIGIN || 'https://lazu.ai/docs',
  repoUrl: process.env.REPO_URL || 'https://github.com/lazu-ai/lazu',
  brandName: process.env.BRAND_NAME || 'Lazu',
  defaultPublicLang: normalizePublicLanguage(
    process.env.DEFAULT_PUBLIC_LANG || 'en',
  ),
  // Default ON; set PUBLIC_INDEXING=false at build time for ephemeral previews.
  publicIndexing: process.env.PUBLIC_INDEXING !== 'false',
};

export const readMarketingContent = (language = env.defaultPublicLang) => {
  const normalized = normalizePublicLanguage(language);
  return JSON.parse(
    readFileSync(
      path.join(appRoot, 'src/marketing', `marketing.${normalized}.json`),
      'utf8',
    ),
  );
};

const marketing = readMarketingContent();

export const absoluteUrl = (pathname) => `${env.siteOrigin}${pathname}`;

export const xmlEscape = (value) =>
  String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&apos;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;');

export const htmlEscape = (value) =>
  String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;');

export const marketingLocales = ['en', 'zh', 'zh-TW', 'ja'];

export const localizedPath = (barePath, locale) => {
  if (locale === 'en') return barePath;
  return barePath === '/' ? `/${locale}/` : `/${locale}${barePath}`;
};

const routeMeta = [
  { barePath: '/', section: 'landing', changefreq: 'weekly', priority: 1.0 },
  { barePath: '/faq', section: 'faq', changefreq: 'weekly', priority: 0.8 },
  {
    barePath: '/about',
    section: 'about',
    changefreq: 'monthly',
    priority: 0.5,
  },
  {
    barePath: '/privacy',
    section: 'privacy',
    changefreq: 'yearly',
    priority: 0.4,
  },
  {
    barePath: '/terms',
    section: 'terms',
    changefreq: 'yearly',
    priority: 0.4,
  },
];

// One route entry per (page, locale): 5 pages x 4 locales. Titles and
// descriptions come from that locale's marketing copy.
export const publicRoutesFor = (locale) => {
  const content = readMarketingContent(locale);
  return routeMeta.map((meta) => ({
    barePath: meta.barePath,
    changefreq: meta.changefreq,
    description: content[meta.section].meta.description,
    locale,
    path: localizedPath(meta.barePath, locale),
    priority:
      locale === 'en'
        ? meta.priority
        : Number((meta.priority * 0.8).toFixed(2)),
    title: content[meta.section].meta.title,
  }));
};

export const allPublicRoutes = () =>
  marketingLocales.flatMap((locale) => publicRoutesFor(locale));

// The publicly indexed public surface. Keep small and explicit; the rest
// of the SPA (console, settings, auth) is auth-only and excluded by robots.
export const publicRoutes = [
  {
    path: '/',
    title: marketing.landing.meta.title,
    description: marketing.landing.meta.description,
    changefreq: 'weekly',
    priority: 1.0,
  },
  {
    path: '/faq',
    title: marketing.faq.meta.title,
    description: marketing.faq.meta.description,
    changefreq: 'weekly',
    priority: 0.8,
  },
  {
    path: '/privacy',
    title: marketing.privacy.meta.title,
    description: marketing.privacy.meta.description,
    changefreq: 'yearly',
    priority: 0.4,
  },
  {
    path: '/terms',
    title: marketing.terms.meta.title,
    description: marketing.terms.meta.description,
    changefreq: 'yearly',
    priority: 0.4,
  },
];
