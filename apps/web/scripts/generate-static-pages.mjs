// Build-time React SSG for the public marketing and legal-document surface.
//
// The production React components are the only source of truth for both the
// initial HTML and the interactive UI. This script renders those components
// into #root for crawlers and first paint; matching clients hydrate that exact
// tree, while a language/theme mismatch is removed before paint and rendered
// normally on the client.
//
// Fallback copy comes from src/marketing/marketing.{lang}.json. Set
// DEFAULT_PUBLIC_LANG to choose the static HTML language at build time.

import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { createServer } from 'vite';
import { loadBuildExtensions } from './extensions.mjs';
import {
  allPublicRoutes,
  env,
  htmlEscape,
  localizedPath,
  marketingLocales,
  paths,
  readMarketingContent,
} from './marketing-utils.mjs';

const indexPath = path.join(paths.distDir, 'index.html');

if (!existsSync(indexPath)) {
  throw new Error(`dist index not found: ${indexPath}`);
}

const baseHtml = readFileSync(indexPath, 'utf8');

// Marketing pages are lazy chunks (src/lib/marketing-pages.tsx). A
// prerendered page links its page's stylesheets, so it is styled before any
// script runs, and preloads its scripts, so hydration does not wait on a
// second round trip. Keep this map aligned with marketing-pages.tsx.
const MARKETING_PAGE_ENTRIES = {
  '/': 'src/pages/Home/index.tsx',
  '/faq': 'src/pages/FAQ/index.tsx',
  '/about': 'src/pages/About/index.tsx',
  '/privacy': 'src/pages/PrivacyPolicy/index.tsx',
  '/terms': 'src/pages/UserAgreement/index.tsx',
};
const buildManifest = JSON.parse(
  readFileSync(path.join(paths.distDir, '.vite', 'manifest.json'), 'utf8'),
);
const baseHtmlAssets = new Set(
  [...baseHtml.matchAll(/(?:href|src)="\/([^"?]+)"/g)].map((match) => match[1]),
);

const marketingPageTags = (routePath) => {
  const bare = routePath.replace(/^\/(zh-TW|zh|ja)(?=\/|$)/, '') || '/';
  const key =
    MARKETING_PAGE_ENTRIES[bare === '/' ? '/' : bare.replace(/\/$/, '')];
  if (!key || !buildManifest[key]) {
    throw new Error(
      `no marketing page chunk for prerendered route ${routePath}`,
    );
  }
  const css = new Set();
  const scripts = new Set();
  const visit = (manifestKey) => {
    const item = buildManifest[manifestKey];
    if (!item || item.isEntry) return;
    if (scripts.has(item.file)) return;
    scripts.add(item.file);
    for (const file of item.css || []) css.add(file);
    for (const imported of item.imports || []) visit(imported);
  };
  visit(key);
  return [
    ...[...css]
      .filter((file) => !baseHtmlAssets.has(file))
      .map((file) => `<link rel="stylesheet" crossorigin href="/${file}" />`),
    ...[...scripts]
      .filter((file) => !baseHtmlAssets.has(file))
      .map(
        (file) => `<link rel="modulepreload" crossorigin href="/${file}" />`,
      ),
  ].join('\n    ');
};
// Per-locale public copy: each prerendered variant draws its meta and
// JSON-LD from its own language's content.
const contentByLocale = Object.fromEntries(
  marketingLocales.map((locale) => [locale, readMarketingContent(locale)]),
);
const marketing = contentByLocale[env.defaultPublicLang];
const brandName = marketing.brand.name;
const landing = marketing.landing;
const ogLocaleFor = (language) =>
  ({
    en: 'en_US',
    zh: 'zh_CN',
    'zh-TW': 'zh_TW',
    ja: 'ja_JP',
  })[language] || 'en_US';

// ─── JSON-LD ─────────────────────────────────────────────────────────────────

// One entity for the whole site: the docs (lazu.ai/docs) and the API-rendered
// /models pages reference this same @id. The operator's legal name
// lives here and on /about, never in titles.
const organizationId = `${env.siteOrigin}/#organization`;
const websiteId = `${env.siteOrigin}/#website`;

const organizationNode = {
  '@type': 'Organization',
  '@id': organizationId,
  name: brandName,
  alternateName: brandName.toLowerCase(),
  legalName: 'DUBRIO LLC',
  url: `${env.siteOrigin}/`,
  logo: {
    '@type': 'ImageObject',
    url: `${env.siteOrigin}/android-chrome-512x512.png`,
    width: 512,
    height: 512,
  },
  email: env.supportEmail,
  foundingDate: '2025',
  address: {
    '@type': 'PostalAddress',
    addressRegion: 'DE',
    addressCountry: 'US',
  },
  contactPoint: {
    '@type': 'ContactPoint',
    contactType: 'customer support',
    email: env.supportEmail,
    availableLanguage: ['en', 'zh', 'ja'],
  },
  sameAs: [
    'https://x.com/try_lazu',
    ...(env.communityUrl ? [env.communityUrl] : []),
  ],
};

const websiteNode = {
  '@type': 'WebSite',
  '@id': websiteId,
  name: brandName,
  url: `${env.siteOrigin}/`,
  publisher: { '@id': organizationId },
};

const softwareApplicationFor = (locale) => ({
  '@type': 'SoftwareApplication',
  '@id': `${env.siteOrigin}/#software`,
  name: brandName,
  applicationCategory: 'DeveloperApplication',
  operatingSystem: 'Web',
  url: env.siteOrigin,
  description: contentByLocale[locale].landing.meta.description,
  // Feature titles from the live landing schema. The previous shape
  // (stats values + string features + a license line) no longer exists —
  // interpolating it emitted "undefined models available" style entries.
  featureList: contentByLocale[locale].landing.features.items.map(
    (item) => item.title,
  ),
  publisher: { '@id': organizationId },
});

const breadcrumbFor = (route) => {
  const items = [
    {
      '@type': 'ListItem',
      position: 1,
      name: brandName,
      item: `${env.siteOrigin}/`,
    },
  ];
  if (route.barePath !== '/') {
    items.push({
      '@type': 'ListItem',
      position: 2,
      name: route.title,
      item: `${env.siteOrigin}${route.path}`,
    });
  }
  return { '@type': 'BreadcrumbList', itemListElement: items };
};

const jsonLdFor = (route) => {
  const canonical = `${env.siteOrigin}${route.path}`;
  const webpageNode = {
    '@type': route.barePath === '/about' ? 'AboutPage' : 'WebPage',
    '@id': `${canonical}#webpage`,
    name: route.title,
    description: route.description,
    url: canonical,
    inLanguage: route.locale,
    isPartOf: { '@id': websiteId },
    breadcrumb: breadcrumbFor(route),
  };
  const graph = [organizationNode, websiteNode, webpageNode];
  if (route.barePath === '/') graph.push(softwareApplicationFor(route.locale));
  if (route.barePath === '/faq') {
    graph.push({
      '@type': 'FAQPage',
      '@id': `${canonical}#faq`,
      inLanguage: route.locale,
      mainEntity: contentByLocale[route.locale].faq.items.map((item) => ({
        '@type': 'Question',
        name: item.q,
        acceptedAnswer: {
          '@type': 'Answer',
          text: item.a,
        },
      })),
    });
  }
  return { '@context': 'https://schema.org', '@graph': graph };
};

// ─── head injection ──────────────────────────────────────────────────────────

const renderAlternates = (route) =>
  [
    ...marketingLocales.map(
      (locale) =>
        `<link rel="alternate" hreflang="${locale}" href="${htmlEscape(
          env.siteOrigin + localizedPath(route.barePath, locale),
        )}" />`,
    ),
    `<link rel="alternate" hreflang="x-default" href="${htmlEscape(
      env.siteOrigin + route.barePath,
    )}" />`,
  ].join('\n    ');

const renderHead = (route) => {
  const canonical = `${env.siteOrigin}${route.path}`;
  const robots = env.publicIndexing ? 'index, follow' : 'noindex, follow';
  return `<!-- Primary Meta Tags -->
    <title>${htmlEscape(route.title)}</title>
    <meta name="title" content="${htmlEscape(route.title)}" />
    <meta name="description" content="${htmlEscape(route.description)}" />
    <meta name="author" content="${htmlEscape(brandName)}" />
    <meta name="robots" content="${robots}" />
    <link rel="canonical" href="${htmlEscape(canonical)}" />
    ${renderAlternates(route)}

    <meta property="og:type" content="website" />
    <meta property="og:url" content="${htmlEscape(canonical)}" />
    <meta property="og:title" content="${htmlEscape(route.title)}" />
    <meta property="og:description" content="${htmlEscape(route.description)}" />
    <meta property="og:image" content="${htmlEscape(env.siteOrigin)}/og-image.png" />
    <meta property="og:image:width" content="1200" />
    <meta property="og:image:height" content="630" />
    <meta property="og:site_name" content="${htmlEscape(brandName)}" />
    <meta property="og:locale" content="${htmlEscape(ogLocaleFor(route.locale))}" />

    <meta name="twitter:card" content="summary_large_image" />
    <meta name="twitter:site" content="@try_lazu" />
    <meta name="twitter:url" content="${htmlEscape(canonical)}" />
    <meta name="twitter:title" content="${htmlEscape(route.title)}" />
    <meta name="twitter:description" content="${htmlEscape(route.description)}" />
    <meta name="twitter:image" content="${htmlEscape(env.siteOrigin)}/og-image.png" />

    <script id="marketing-jsonld" type="application/ld+json">${JSON.stringify(jsonLdFor(route))}</script>`;
};

const renderHydrationGuard = () => `<script>
      (() => {
        const root = document.getElementById('root');
        if (!root) return;

        const normalizeLanguage = (value) => {
          const normalized = String(value || '').replace(/_/g, '-');
          if (/^zh[-]?(tw|hk|hant|mo)/i.test(normalized)) return 'zh-TW';
          if (/^zh(?:-|$)/i.test(normalized)) return 'zh';
          return normalized.split('-')[0].toLowerCase() || 'en';
        };

        const normalizePath = (value) => {
          const normalized = String(value || '/').replace(/\\/+$/, '');
          return normalized || '/';
        };

        const readCookie = (name) => {
          try {
            const escaped = name.replace(/[.*+?^\${}()|[\]\\]/g, '\\$&');
            const match = document.cookie.match(
              new RegExp('(?:^|;\\s*)' + escaped + '=([^;]*)'),
            );
            return match ? decodeURIComponent(match[1]) : '';
          } catch (_) {
            return '';
          }
        };

        const readQueryLanguage = () => {
          try {
            let query = window.location.search;
            if (!query && window.location.hash.includes('?')) {
              query = window.location.hash.slice(
                window.location.hash.indexOf('?'),
              );
            }
            return new URLSearchParams(query).get('lng') || '';
          } catch (_) {
            return '';
          }
        };

        let localLanguage = '';
        let sessionLanguage = '';
        let storedTheme = 'auto';
        try {
          localLanguage = localStorage.getItem('i18nextLng') || '';
          storedTheme =
            localStorage.getItem('lazu.theme') ||
            localStorage.getItem('theme-mode') ||
            'auto';
        } catch (_) {}
        try {
          sessionLanguage = sessionStorage.getItem('i18nextLng') || '';
        } catch (_) {}

        // A locale-prefixed URL asserts its language — it beats every
        // stored preference, matching src/i18n/i18n.ts.
        const pathLanguage = (() => {
          const match = /^\\/(zh-TW|zh|ja)(?=\\/|$)/.exec(
            window.location.pathname,
          );
          return match ? match[1] : '';
        })();

        const language = normalizeLanguage(
          pathLanguage ||
            readQueryLanguage() ||
            readCookie('i18next') ||
            localLanguage ||
            sessionLanguage ||
            navigator.languages?.[0] ||
            navigator.language ||
            document.documentElement.lang ||
            'en',
        );
        const prerenderLanguage = normalizeLanguage(
          root.dataset.prerenderLanguage || 'en',
        );
        const prerenderPath = normalizePath(
          root.dataset.prerenderRoute || '/',
        );
        const currentPath = normalizePath(window.location.pathname);
        const systemDark =
          window.matchMedia &&
          window.matchMedia('(prefers-color-scheme: dark)').matches;
        const actualTheme =
          storedTheme === 'auto' ? (systemDark ? 'dark' : 'light') : storedTheme;

        if (
          currentPath === prerenderPath &&
          language === prerenderLanguage &&
          actualTheme === 'light'
        ) {
          root.dataset.hydrate = 'true';
          return;
        }

        // The 404 page answers every unknown address, so its URL never
        // matches. Keep it in place: the client renders it afresh and
        // replaces the whole root in one commit once the page is ready
        // (src/index.tsx); clearing it here blanked the page first. In
        // another language or theme it keeps its space but is not shown,
        // so the wrong copy never flashes and nothing moves.
        if (prerenderPath === '/__not-found') {
          if (language !== prerenderLanguage || actualTheme !== 'light') {
            root.style.visibility = 'hidden';
          }
          return;
        }

        if (currentPath === prerenderPath) {
          root.style.visibility = 'hidden';
        } else {
          // SPA fallback HTML is not a snapshot of this destination.
          root.replaceChildren();
          delete root.dataset.prerenderRoute;
          delete root.dataset.prerenderLanguage;
          delete root.dataset.prerenderTheme;
        }
      })();
    </script>`;

const inject = async (route, renderMarketingRoute) => {
  const prerenderedBody = await renderMarketingRoute(route.path, route.locale);
  const prerenderedRoot = `<div id="root" data-prerender-route="${htmlEscape(route.path)}" data-prerender-language="${htmlEscape(route.locale)}" data-prerender-theme="light">${prerenderedBody}</div>\n    ${renderHydrationGuard()}`;

  return baseHtml
    .replace(/<html lang="[^"]*">/, `<html lang="${htmlEscape(route.locale)}">`)
    .replace(
      /<!-- Primary Meta Tags -->[\s\S]*?<!-- SSG Head Anchor -->/,
      () =>
        `${renderHead(route)}\n    ${marketingPageTags(route.path)}\n\n    <!-- SSG Head Anchor -->`,
    )
    .replace(
      '<div id="root"></div>',
      // Use a replacer callback so `$&` inside the hydration guard remains
      // literal JavaScript. Passing the generated HTML as a replacement
      // string makes String.replace expand `$&` to the matched root element,
      // corrupting the guard before it reaches the browser.
      () => prerenderedRoot,
    );
};

const writeRoute = async (route, renderMarketingRoute) => {
  const html = await inject(route, renderMarketingRoute);
  if (route.path === '/') {
    writeFileSync(indexPath, html);
    return;
  }
  const routeDir = path.join(
    paths.distDir,
    route.path.replace(/^\/+/, '').replace(/\/+$/, ''),
  );
  mkdirSync(routeDir, { recursive: true });
  writeFileSync(path.join(routeDir, 'index.html'), html);
};

const vite = await createServer({
  appType: 'custom',
  logLevel: 'error',
  root: paths.appRoot,
  server: { hmr: false, middlewareMode: true, ws: false },
});

// ─── chrome partials for the API-rendered pages ─────────────────────────────
//
// /models and the legal pages are rendered by the API, and so are pages
// extensions add. Their pages include these files through nginx SSI
// (`<!--# include virtual="/__chrome/…" -->`), so the header, footer and
// stylesheets are the very ones this build produced.
const CHROME_SECTIONS = Object.assign(
  {
    models: '/models',
    privacy: '/privacy',
    terms: '/terms',
  },
  ...(await loadBuildExtensions(paths.appRoot)).map(
    (extension) => extension.chromeSections ?? {},
  ),
);

const readSiteEntryTags = () => {
  const manifestPath = path.join(paths.distDir, '.vite', 'manifest.json');
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  const entry = manifest['src/public-site/entry.ts'];
  if (!entry) {
    throw new Error('public-site entry missing from the Vite manifest');
  }
  const css = new Set(entry.css || []);
  // Stylesheets of statically imported chunks load with the entry too.
  for (const key of entry.imports || []) {
    for (const file of manifest[key]?.css || []) css.add(file);
  }
  return [
    ...[...css].map(
      (file) => `<link rel="stylesheet" crossorigin href="/${file}" />`,
    ),
    `<script type="module" crossorigin src="/${entry.file}"></script>`,
  ].join('\n');
};

// Runs before first paint, like the inline script in index.html, so a dark
// visitor never sees a light flash.
const themeBoot = `<script>
  (() => {
    try {
      const stored =
        localStorage.getItem('lazu.theme') ||
        localStorage.getItem('theme-mode') ||
        'auto';
      const systemDark =
        window.matchMedia &&
        window.matchMedia('(prefers-color-scheme: dark)').matches;
      const theme = stored === 'auto' ? (systemDark ? 'dark' : 'light') : stored;
      document.body.setAttribute('theme-mode', theme);
      document.documentElement.classList.toggle('dark', theme === 'dark');
    } catch (_) {
      document.body.setAttribute('theme-mode', 'light');
    }
  })();
</script>`;

const writeChromePartials = async (renderPublicChrome) => {
  const dir = path.join(paths.distDir, '__chrome');
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, 'head.html'), `${readSiteEntryTags()}\n`);
  writeFileSync(path.join(dir, 'boot.html'), `${themeBoot}\n`);
  for (const locale of marketingLocales) {
    for (const [name, section] of Object.entries(CHROME_SECTIONS)) {
      for (const part of ['header', 'footer']) {
        const html = await renderPublicChrome(part, locale, section);
        writeFileSync(path.join(dir, `${part}.${locale}.${name}.html`), html);
      }
    }
  }
  console.log(
    `[seo] wrote chrome partials for ${marketingLocales.length} locales`,
  );
};

// The single-page app (console, sign-in, payment returns …) boots from an
// unprerendered shell. index.html becomes the prerendered home page below, so
// the shell gets its own file and nginx serves it only for app-owned paths.
const writeAppShell = () => {
  const shell = baseHtml.replace(
    /<!-- Primary Meta Tags -->[\s\S]*?<!-- SSG Head Anchor -->/,
    `<title>${htmlEscape(brandName)}</title>
    <meta name="robots" content="noindex, nofollow" />

    <!-- SSG Head Anchor -->`,
  );
  writeFileSync(path.join(paths.distDir, 'app.html'), shell);
};

// Unknown addresses answer 404 with this page: the public chrome around the
// locked StatusPage. The hydration guard sees the real URL differ from the
// prerender route and lets the client render NotFound for it.
const writeNotFound = async (renderNotFoundPage) => {
  const route = '/__not-found';
  const body = await renderNotFoundPage('en');
  const html = baseHtml
    .replace(
      /<!-- Primary Meta Tags -->[\s\S]*?<!-- SSG Head Anchor -->/,
      `<title>404 · ${htmlEscape(brandName)}</title>
    <meta name="robots" content="noindex, follow" />

    <!-- SSG Head Anchor -->`,
    )
    .replace(
      '<div id="root"></div>',
      () =>
        `<div id="root" data-prerender-route="${route}" data-prerender-language="en" data-prerender-theme="light">${body}</div>\n    ${renderHydrationGuard()}`,
    );
  writeFileSync(path.join(paths.distDir, '404.html'), html);
};

writeAppShell();

try {
  const prerenderModule = await vite.ssrLoadModule('/src/entry-prerender.tsx');
  if (typeof prerenderModule.renderMarketingRoute !== 'function') {
    throw new Error(
      'React marketing prerender entry did not export a renderer',
    );
  }
  for (const route of allPublicRoutes()) {
    await writeRoute(route, prerenderModule.renderMarketingRoute);
  }
  await writeNotFound(prerenderModule.renderNotFoundPage);
  if (typeof prerenderModule.renderPublicChrome === 'function') {
    await writeChromePartials(prerenderModule.renderPublicChrome);
  }
} finally {
  await vite.close();
}

const routes = allPublicRoutes();
console.log(
  `[seo] prerendered ${routes.length} public routes (${routes.map((r) => r.path).join(', ')})`,
);
