// Post-build GEO pass over the prerendered docs HTML.
//
// Vocs hard-codes `<html lang="en">` in its render template (see
// vocs/_lib/vite/utils/html.js), ships no canonical, no og:url and no
// structured data. All four are per-document facts that are easiest to fix
// where the documents already exist — so this walks dist/**/*.html and, for
// each page:
//   1. rewrites the lang attribute to match the locale directory,
//   2. injects <link rel="canonical"> + og:url,
//   3. injects one JSON-LD @graph: Organization, WebSite, TechArticle
//      (headline/description read from the page's own <title>/meta) and a
//      BreadcrumbList derived from the route,
//   4. links every locale version of the route (hreflang + x-default) and the
//      route's Markdown sibling (rel=alternate type=text/markdown), so search
//      engines stop ranking the four languages against each other and agents
//      find the clean copy without parsing the SPA shell.
//
// Client-side navigations keep lang correct via agent-actions.js.
import { readFile, readdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const appDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const distDir = path.join(appDir, "dist");
// The docs live under lazu.ai/docs; every absolute URL carries that base.
const siteOrigin = (
  process.env.NEXT_PUBLIC_SITE_ORIGIN ||
  process.env.SITE_ORIGIN ||
  "https://lazu.ai/docs"
).replace(/\/+$/, "");

// Canonical docs URLs are slashless, the docs home included
// (https://lazu.ai/docs, as the sitemap and nginx have it); locale roots
// keep their slash (/zh/).
function absolute(route: string): string {
  return route === "/" ? siteOrigin : `${siteOrigin}${route}`;
}

const ORGANIZATION = {
  "@type": "Organization",
  "@id": "https://lazu.ai/#organization",
  name: "Lazu",
  url: "https://lazu.ai/",
  sameAs: ["https://x.com/try_lazu"],
};

const WEBSITE = {
  "@type": "WebSite",
  "@id": `${siteOrigin}/#website`,
  name: "Lazu Docs",
  url: siteOrigin,
  publisher: { "@id": "https://lazu.ai/#organization" },
};

function langFor(route: string): string {
  if (route === "/ja" || route.startsWith("/ja/")) return "ja";
  if (route === "/zh-TW" || route.startsWith("/zh-TW/")) return "zh-TW";
  if (route === "/zh" || route.startsWith("/zh/")) return "zh";
  return "en";
}

async function listHtml(directory: string, prefix = ""): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map(async (entry) => {
      const relative = path.join(prefix, entry.name);
      if (entry.isDirectory())
        return listHtml(path.join(directory, entry.name), relative);
      return entry.isFile() && entry.name.endsWith(".html") ? [relative] : [];
    }),
  );
  return files.flat();
}

function routeFor(file: string): string {
  const posix = file.split(path.sep).join("/");
  if (posix === "index.html") return "/";
  // Directory-style output maps to the slashless URL the sitemap declares
  // (nginx serves both; canonical must agree with the sitemap).
  if (posix.endsWith("/index.html"))
    return `/${posix.slice(0, -"/index.html".length)}`;
  return `/${posix.slice(0, -".html".length)}`;
}

const LOCALE_PREFIXES = ["zh-TW", "zh", "ja"] as const;

// "/zh/endpoints/chat" -> "/endpoints/chat"; locale roots map to "/".
function bareRoute(route: string): string {
  for (const prefix of LOCALE_PREFIXES) {
    if (route === `/${prefix}/` || route === `/${prefix}`) return "/";
    if (route.startsWith(`/${prefix}/`)) return route.slice(prefix.length + 1);
  }
  return route;
}

function localizedRoute(bare: string, lang: string): string {
  if (lang === "en") return bare;
  return bare === "/" ? `/${lang}/` : `/${lang}${bare}`;
}

function markdownRoute(route: string): string {
  if (route === "/") return "/index.md";
  if (route.endsWith("/")) return `${route}index.md`;
  return `${route}.md`;
}

function decodeEntities(value: string): string {
  return value
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&#39;/g, "'");
}

function breadcrumb(route: string, url: string, title: string) {
  const segments = route.split("/").filter(Boolean);
  const items = [
    {
      "@type": "ListItem",
      position: 1,
      name: "Lazu Docs",
      item: siteOrigin,
    },
  ];
  if (segments.length) {
    items.push({
      "@type": "ListItem",
      position: 2,
      name: title,
      item: url,
    });
  }
  return { "@type": "BreadcrumbList", itemListElement: items };
}

async function run(): Promise<void> {
  const files = await listHtml(distDir);
  let patched = 0;

  // Which locales ship each route, from the files that actually exist.
  const localesByRoute = new Map<string, Set<string>>();
  for (const file of files) {
    let route = routeFor(file);
    if (["/zh", "/zh-TW", "/ja"].includes(route)) route = `${route}/`;
    const bare = bareRoute(route);
    const set = localesByRoute.get(bare) ?? new Set<string>();
    set.add(langFor(route));
    localesByRoute.set(bare, set);
  }

  for (const file of files) {
    const filePath = path.join(distDir, file);
    let html = await readFile(filePath, "utf8");
    if (html.includes('rel="canonical"')) continue; // already processed

    let route = routeFor(file);
    if (["/zh", "/zh-TW", "/ja"].includes(route)) route = `${route}/`;
    const lang = langFor(route);
    const url = absolute(route);
    const title = decodeEntities(
      html.match(/<title>([^<]*)<\/title>/)?.[1] || "Lazu Docs",
    ).replace(/\s+–\s+Lazu API Docs$/, "");
    const description = decodeEntities(
      html.match(/name="description" content="([^"]*)"/)?.[1] || "",
    );

    const graph = {
      "@context": "https://schema.org",
      "@graph": [
        ORGANIZATION,
        WEBSITE,
        {
          "@type": "TechArticle",
          headline: title,
          ...(description ? { description } : {}),
          inLanguage: lang,
          url,
          isPartOf: { "@id": `${siteOrigin}/#website` },
          author: { "@id": "https://lazu.ai/#organization" },
        },
        breadcrumb(route, url, title),
      ],
    };

    const bare = bareRoute(route);
    const available = localesByRoute.get(bare) ?? new Set([lang]);
    const alternates = ["en", "zh", "zh-TW", "ja"]
      .filter((code) => available.has(code))
      .map(
        (code) =>
          `<link rel="alternate" hreflang="${code}" href="${absolute(localizedRoute(bare, code))}"/>`,
      )
      .join("");
    const xDefault = available.has("en")
      ? `<link rel="alternate" hreflang="x-default" href="${absolute(bare)}"/>`
      : "";

    const inject =
      `<link rel="canonical" href="${url}"/>` +
      alternates +
      xDefault +
      `<link rel="alternate" type="text/markdown" href="${absolute(markdownRoute(route))}"/>` +
      `<meta property="og:url" content="${url}"/>` +
      // Vocs ships no og:image; reuse the marketing card so shared docs
      // links unfurl with the same face as lazu.ai.
      `<meta property="og:image" content="https://lazu.ai/og-image.png"/>` +
      `<meta name="twitter:site" content="@try_lazu"/>` +
      `<script type="application/ld+json">${JSON.stringify(graph)}</script>`;

    html = html
      .replace(/<html lang="en"/, `<html lang="${lang}"`)
      .replace("</head>", `${inject}</head>`);
    await writeFile(filePath, html, "utf8");
    patched += 1;
  }

  console.log(
    `[docs] postbuild geo: ${patched}/${files.length} pages — lang, canonical, hreflang, markdown alternate, og:url, JSON-LD`,
  );
}

await run();
