// Build-time generator for static SEO assets shipped from /public:
//   - robots.txt          (with explicit AI-crawler allowlist)
//   - llms.txt            (LLM-friendly site overview)
//   - llms-full.txt       (extended LLM context, full home copy)
//   - sitemap.xml         (canonical URL list for search engines)
//
// English is unprefixed; zh, zh-TW and ja have crawlable URL prefixes. The
// route helpers below generate canonical, alternate and markdown siblings
// from that single locale map.

import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import {
  allPublicRoutes,
  env,
  localizedPath,
  marketingLocales,
  paths,
  publicRoutes,
  readMarketingContent,
  xmlEscape,
} from './marketing-utils.mjs';
import { loadBuildExtensions } from './extensions.mjs';

mkdirSync(paths.publicDir, { recursive: true });

const buildExtensions = await loadBuildExtensions(
  path.resolve(import.meta.dirname, '..'),
);
const siteHost = new URL(env.siteOrigin).host;
const marketing = readMarketingContent();
const brandName = marketing.brand.name;
const landing = marketing.landing;
const faq = marketing.faq;
const lede = `${landing.hero.whatBefore}${landing.hero.whatBold}${landing.hero.whatAfter}`;
// The page splits the headline into a roman lead and an italic close purely for
// presentation; every generated text surface carries the whole sentence.
const fullHeadline = (hero) => `${hero.headline}${hero.headlineEm || ''}`;

const robots = `# robots.txt for ${siteHost}
User-agent: *
Allow: /
Disallow: /console/
Disallow: /app/
Disallow: /admin/
Disallow: /api/
Disallow: /auth/
Disallow: /login
Disallow: /register

# AI crawlers — explicitly allowed for AI search citation
User-agent: GPTBot
User-agent: ChatGPT-User
User-agent: ClaudeBot
User-agent: Claude-Web
User-agent: anthropic-ai
User-agent: PerplexityBot
User-agent: Google-Extended
User-agent: Amazonbot
User-agent: Bytespider
User-agent: CCBot
User-agent: cohere-ai
User-agent: OAI-SearchBot
User-agent: Perplexity-User
User-agent: Applebot-Extended
User-agent: meta-externalagent
Allow: /
Disallow: /console/
Disallow: /app/
Disallow: /admin/
Disallow: /api/
Disallow: /auth/
Disallow: /login
Disallow: /register

Sitemap: ${env.siteOrigin}/sitemap.xml
`;

const llms = `# ${brandName}

> ${landing.meta.description}

## Key facts

- OpenAI-compatible at \`${env.apiOrigin}/v1/*\` — change \`base_url\` and reuse official SDKs
- Native provider endpoints also supported (Anthropic Messages and Gemini)
- ${landing.features.items.map((item) => item.title).join('\n- ')}
- Automatic failover on upstream error

## Pages

- [Home](${env.siteOrigin}/): product overview \u00b7 [\u4e2d\u6587](${env.siteOrigin}/zh/) \u00b7 [\u7e41\u9ad4](${env.siteOrigin}/zh-TW/) \u00b7 [\u65e5\u672c\u8a9e](${env.siteOrigin}/ja/)
- [FAQ](${env.siteOrigin}/faq): common questions about migration, payment, models
- [About](${env.siteOrigin}/about): who operates Lazu (DUBRIO LLC, Delaware) and how to reach us
- [Privacy Policy](${env.siteOrigin}/privacy): how Lazu handles account, API usage, OAuth, and payment information
- [Terms of Service](${env.siteOrigin}/terms): terms for accounts, gateway usage, and billing

## Models

- [Model catalog](${env.siteOrigin}/models): every model with live prices on both lanes, context length and capabilities ([Markdown](${env.siteOrigin}/models.md))
- Each model has its own page, e.g. \`${env.siteOrigin}/models/anthropic/<model>\`, with a \`.md\` and a \`.json\` sibling
- Machine-readable models and prices: [${env.siteOrigin}/models.json](${env.siteOrigin}/models.json)

${buildExtensions.map((extension) => extension.llmsSections?.(env) ?? '').join('')}## Documentation

- [Docs](${env.docsOrigin}): quickstart, examples, API reference
- [API Reference](${env.docsOrigin}/api-reference): interactive OpenAPI reference
- [Examples](${env.docsOrigin}/examples): cURL / Python / JavaScript / TypeScript / Go
- [Changelog](${env.docsOrigin}/changelog): API updates
`;

const llmsFull = `${llms}
## Home page content

${fullHeadline(landing.hero)}

${lede}

### ${landing.features.title}

${landing.features.items.map((item) => `- ${item.title}：${item.body}`).join('\n')}

### ${landing.steps.title}

${landing.steps.items.map((item) => `- ${item.title}：${item.body}`).join('\n')}

## FAQ

${faq.items.map((item) => `### ${item.q}\n\n${item.a}`).join('\n\n')}
`;

// Every (page, locale) URL, each carrying the full hreflang set so the four
// language versions cross-reference each other (same pattern as docs.lazu.ai).
const alternatesFor = (barePath) =>
  [
    ...marketingLocales.map(
      (locale) =>
        `    <xhtml:link rel="alternate" hreflang="${locale}" href="${xmlEscape(
          env.siteOrigin + localizedPath(barePath, locale),
        )}" />`,
    ),
    `    <xhtml:link rel="alternate" hreflang="x-default" href="${xmlEscape(
      env.siteOrigin + barePath,
    )}" />`,
  ].join('\n');

// No <lastmod>: these pages are rebuilt on every deploy, so the build date
// would claim every page changed each time, and search engines stop trusting
// lastmod on sites where it is wrong. The API's sitemaps carry real dates.
const sitemap = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">
${allPublicRoutes()
  .map(
    (route) =>
      `  <url>\n` +
      `    <loc>${xmlEscape(env.siteOrigin + route.path)}</loc>\n` +
      `${alternatesFor(route.barePath)}\n` +
      `    <changefreq>${xmlEscape(route.changefreq)}</changefreq>\n` +
      `    <priority>${route.priority}</priority>\n` +
      `  </url>`,
  )
  .join('\n')}
</urlset>
`;

// The index points at the static pages above plus the sitemaps the API
// renders for the live catalog and for extensions' pages, and the docs' own
// sitemap.
const sitemapIndex = `<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${[
  `${env.siteOrigin}/sitemap-pages.xml`,
  `${env.siteOrigin}/models/sitemap.xml`,
  ...buildExtensions.flatMap((extension) => extension.sitemaps?.(env) ?? []),
  `${env.docsOrigin}/sitemap.xml`,
]
  .map((loc) => `  <sitemap><loc>${xmlEscape(loc)}</loc></sitemap>`)
  .join('\n')}
</sitemapindex>
`;

// Per-page markdown siblings (/, /faq, /privacy, /terms), mirroring
// docs.lazu.ai: a clean text form per URL so AI
// retrievers don't have to parse the SPA shell.
const mdNameFor = (route) => {
  const bare = route.barePath === '/' ? '/index' : route.barePath;
  return `${localizedPath(bare, route.locale).replace(/^\//, '')}.md`;
};

const legalDefaultFor = (documentName, locale) =>
  readFileSync(
    path.join(
      paths.repoRoot,
      'internal/setting/system_setting/legal_defaults',
      `${documentName}.${locale}.md`,
    ),
    'utf8',
  ).trim();

const pageMdFor = (locale) => {
  const content = readMarketingContent(locale);
  const l = content.landing;
  const f = content.faq;
  const localLede = `${l.hero.whatBefore}${l.hero.whatBold}${l.hero.whatAfter}`;
  const prefix = (bare) => env.siteOrigin + localizedPath(bare, locale);
  return {
    [mdNameFor({ barePath: '/', locale })]:
      `<!-- Lazu \u00b7 ${l.meta.title} \u00b7 ${prefix('/')} -->

# ${fullHeadline(l.hero)}

${localLede}

## ${l.features.title}

${l.features.items.map((item) => `- **${item.title}** \u2014 ${item.body}`).join('\n')}

## ${l.steps.title}

${l.steps.items.map((item, i) => `${i + 1}. **${item.title}** \u2014 ${item.body}`).join('\n')}

OpenAI-compatible endpoint: ${env.apiOrigin}/v1 \u00b7 Docs: ${env.docsOrigin}
`,
    [mdNameFor({ barePath: '/faq', locale })]:
      `<!-- Lazu \u00b7 ${f.meta.title} \u00b7 ${prefix('/faq')} -->

# ${f.meta.title}

${f.items.map((item) => `## ${item.q}\n\n${item.a}`).join('\n\n')}
`,
    [mdNameFor({ barePath: '/about', locale })]:
      `<!-- Lazu \u00b7 ${content.about.meta.title} \u00b7 ${prefix('/about')} -->

# ${content.about.title}

${content.about.lede}

${content.about.facts
  .map(
    (fact) =>
      `- **${fact.label}:** ${fact.value.replace('[[terms]]', content.about.termsLink)}`,
  )
  .join('\n')}

## ${content.about.machineTitle}

${content.about.machine.map((entry) => `- \`${entry.path}\` \u2014 ${entry.text}`).join('\n')}
`,
    [mdNameFor({ barePath: '/privacy', locale })]:
      `<!-- Lazu · ${content.privacy.meta.title} · ${prefix('/privacy')} -->

${legalDefaultFor('privacy_policy', locale)}
`,
    [mdNameFor({ barePath: '/terms', locale })]:
      `<!-- Lazu · ${content.terms.meta.title} · ${prefix('/terms')} -->

${legalDefaultFor('user_agreement', locale)}
`,
  };
};

const pageMd = Object.assign({}, ...marketingLocales.map(pageMdFor));

for (const [name, content] of Object.entries(pageMd)) {
  const target = path.join(paths.publicDir, name);
  mkdirSync(path.dirname(target), { recursive: true });
  writeFileSync(target, content);
}

writeFileSync(path.join(paths.publicDir, 'robots.txt'), robots);
writeFileSync(path.join(paths.publicDir, 'llms.txt'), llms);
writeFileSync(path.join(paths.publicDir, 'llms-full.txt'), llmsFull);
writeFileSync(path.join(paths.publicDir, 'sitemap-pages.xml'), sitemap);
writeFileSync(path.join(paths.publicDir, 'sitemap.xml'), sitemapIndex);

console.log(
  `[seo] wrote robots, llms, llms-full, sitemap index for ${env.siteOrigin} (indexing=${env.publicIndexing})`,
);
