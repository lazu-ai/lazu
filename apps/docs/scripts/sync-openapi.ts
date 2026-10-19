import {
  copyFile,
  mkdir,
  readFile,
  readdir,
  writeFile,
} from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";

import { mdxToMarkdown } from "./mdx-markdown";

type FrontmatterData = {
  description?: string;
  title?: string;
};

type FrontmatterResult = {
  body: string;
  data: FrontmatterData;
};

type DocsPage = {
  description: string;
  locale: LocaleCode;
  markdown: string;
  routeKey: string;
  title: string;
  url: string;
};

type AssetTarget = {
  json: string;
  yaml: string;
};

type LocaleCode = "en" | "zh" | "zh-TW" | "ja";

type TagOverlay = Record<string, string>;
type OperationOverlay = {
  summary?: string;
  description?: string;
};
type PathOverlay = Record<string, Record<string, OperationOverlay>>;
type LocaleOverlay = {
  info?: { description?: string };
  tags?: TagOverlay;
  paths?: PathOverlay;
};

// Scalar's UI chrome is hardcoded English (the @scalar config schema has
// no `locale` knob). What we *can* localise is the OpenAPI document the
// playground renders — sidebar groupings (tag descriptions), sidebar
// items (operation summaries), and the prose at the top of each
// operation (operation descriptions). Each locale ships an overlay JSON
// under apps/docs/i18n/openapi/<locale>.json that mirrors the master
// spec's shape; the sync step deep-merges those onto the parsed master
// and writes one JSON per locale into /public.
//
// `/openapi.json` (the canonical URL external integrators may already
// link to) is the English variant — that's also what Lazu's spec is
// intended to be authored in going forward. The locale-specific URLs
// /openapi.{zh,zh-TW,ja}.json carry translated chrome on top of the
// same structural spec.
const LOCALES: readonly LocaleCode[] = ["en", "zh", "zh-TW", "ja"] as const;
const localePrefixes: Record<LocaleCode, string> = {
  en: "",
  zh: "/zh",
  "zh-TW": "/zh-TW",
  ja: "/ja",
};
const i18nDir = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
  "i18n",
  "openapi",
);

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const appDir = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(appDir, "../..");
const sourcePath = path.join(
  repoRoot,
  "packages/contracts/openapi/lazu-api-reference.v1.yaml",
);
const publicDir = path.join(appDir, "public");
// The docs live under lazu.ai/docs, so the "origin" every absolute URL is
// built from carries the /docs base path. Vocs serves public/ under that base,
// so public/llms.txt is https://lazu.ai/docs/llms.txt.
const siteOrigin = trimTrailingSlash(
  process.env.NEXT_PUBLIC_SITE_ORIGIN ||
    process.env.SITE_ORIGIN ||
    "https://lazu.ai/docs",
);

function trimTrailingSlash(value: string): string {
  return value.replace(/\/+$/, "");
}

function absoluteUrl(urlPath: string): string {
  const normalized = urlPath.startsWith("/") ? urlPath : `/${urlPath}`;
  return `${siteOrigin}${normalized === "/" ? "" : normalized}`;
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function parseFrontmatter(content: string): FrontmatterResult {
  if (!content.startsWith("---")) {
    return { body: content, data: {} };
  }

  const end = content.indexOf("\n---", 3);
  if (end === -1) {
    return { body: content, data: {} };
  }

  const raw = content.slice(3, end);
  const body = content.slice(end + 4).trimStart();
  const data = (YAML.parse(raw) as FrontmatterData | null) || {};

  return { body, data };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// Deep-clone a structured-clone-able payload so each locale variant can
// be mutated without leaking changes into the next iteration.
function deepClone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

// Apply a locale overlay onto a parsed OpenAPI document IN PLACE.
//
// Overlay shape mirrors the master spec but only carries translatable
// strings:
//   - info.description
//   - tags: { "<tag-name>": "<translated description>" }
//   - paths: { "<path>": { "<method>": { summary?, description? } } }
//
// Missing keys in the overlay leave the master string untouched (we
// fall back to whatever the canonical spec carries), so partially
// translated locales are safe to ship.
function applyLocaleOverlay(
  spec: Record<string, unknown>,
  overlay: LocaleOverlay,
): void {
  if (overlay.info?.description && isRecord(spec.info)) {
    spec.info.description = overlay.info.description;
  }

  if (overlay.tags && Array.isArray(spec.tags)) {
    for (const tag of spec.tags) {
      if (!isRecord(tag) || typeof tag.name !== "string") continue;
      const translated = overlay.tags[tag.name];
      if (translated) tag.description = translated;
    }
  }

  if (overlay.paths && isRecord(spec.paths)) {
    for (const [pathKey, methods] of Object.entries(overlay.paths)) {
      const specPath = spec.paths[pathKey];
      if (!isRecord(specPath)) continue;
      for (const [method, fields] of Object.entries(methods)) {
        const op = specPath[method];
        if (!isRecord(op)) continue;
        if (fields.summary !== undefined) op.summary = fields.summary;
        if (fields.description !== undefined)
          op.description = fields.description;
      }
    }
  }
}

async function loadOverlay(locale: LocaleCode): Promise<LocaleOverlay> {
  const overlayPath = path.join(i18nDir, `${locale}.json`);
  const raw = await readFile(overlayPath, "utf8");
  return JSON.parse(raw) as LocaleOverlay;
}

async function syncOpenApi(): Promise<void> {
  await mkdir(publicDir, { recursive: true });

  const yamlContent = await readFile(sourcePath, "utf8");
  const parsedMaster = YAML.parse(yamlContent);

  // Compatibility outputs: the original /openapi.json + /openapi.yaml
  // paths stay live so external integrators (Postman, Stoplight, the
  // CI snapshots) don't break. They serve the English variant — the
  // documented canonical for the spec going forward.
  const enOverlay = await loadOverlay("en");
  const enSpec = deepClone(parsedMaster);
  applyLocaleOverlay(enSpec as Record<string, unknown>, enOverlay);
  const enJson = `${JSON.stringify(enSpec, null, 2)}\n`;

  const compatTargets: AssetTarget[] = [
    {
      json: path.join(publicDir, "openapi.json"),
      yaml: path.join(publicDir, "openapi.yaml"),
    },
  ];
  for (const target of compatTargets) {
    await copyFile(sourcePath, target.yaml);
    await writeFile(target.json, enJson, "utf8");
  }

  // Locale-specific outputs the api-reference client picks by URL.
  for (const locale of LOCALES) {
    const overlay = locale === "en" ? enOverlay : await loadOverlay(locale);
    const spec =
      locale === "en"
        ? enSpec
        : (() => {
            const clone = deepClone(parsedMaster);
            applyLocaleOverlay(clone as Record<string, unknown>, overlay);
            return clone;
          })();
    const json = `${JSON.stringify(spec, null, 2)}\n`;
    await writeFile(
      path.join(publicDir, `openapi.${locale}.json`),
      json,
      "utf8",
    );
  }

  console.log(
    `[docs] synced OpenAPI spec to public assets (locales: ${LOCALES.join(", ")})`,
  );
}

async function loadPages(): Promise<DocsPage[]> {
  const rootPagesDir = path.join(appDir, "pages");
  const files = (await listMdxFiles(rootPagesDir)).sort((left, right) => {
    if (left === "index.mdx") return -1;
    if (right === "index.mdx") return 1;
    return pageUrlFor(left).localeCompare(pageUrlFor(right));
  });

  const pages = await Promise.all(
    files.map(async (file) => {
      const content = await readFile(path.join(rootPagesDir, file), "utf8");
      const { body, data } = parseFrontmatter(content);
      const url = pageUrlFor(file);

      return {
        description: data.description || "Lazu public documentation page",
        locale: localeForUrl(url),
        markdown: mdxToMarkdown(body, localeForUrl(url), siteOrigin),
        routeKey: routeKeyForUrl(url),
        title: data.title || path.basename(file, ".mdx"),
        url,
      };
    }),
  );

  const authoredApiReferenceLocales = new Set(
    pages
      .filter((page) => page.routeKey === "/api-reference")
      .map((page) => page.locale),
  );
  for (const locale of LOCALES) {
    if (!authoredApiReferenceLocales.has(locale)) {
      pages.push(apiReferencePage(locale));
    }
  }

  return pages;
}

function apiReferencePage(locale: LocaleCode): DocsPage {
  const localized: Record<
    LocaleCode,
    { description: string; markdown: string; title: string }
  > = {
    en: {
      description:
        "Secondary OpenAPI explorer for raw request and response fields.",
      markdown: `# OpenAPI Explorer

Lazu API raw OpenAPI schema explorer. Use the endpoint guide pages for the primary documentation flow.

- OpenAPI JSON: /openapi.json
- OpenAPI YAML: /openapi.yaml`,
      title: "OpenAPI Explorer",
    },
    zh: {
      description: "Lazu API 原始 OpenAPI schema 浏览器。",
      markdown: `# OpenAPI Explorer

Lazu API 原始 OpenAPI schema 浏览器。主要阅读路径请使用 endpoint guide 页面。

- OpenAPI JSON: /openapi.json
- OpenAPI YAML: /openapi.yaml`,
      title: "OpenAPI Explorer",
    },
    "zh-TW": {
      description: "Lazu API 原始 OpenAPI schema 瀏覽器。",
      markdown: `# OpenAPI Explorer

Lazu API 原始 OpenAPI schema 瀏覽器。主要閱讀路徑請使用 endpoint guide 頁面。

- OpenAPI JSON: /openapi.json
- OpenAPI YAML: /openapi.yaml`,
      title: "OpenAPI Explorer",
    },
    ja: {
      description: "Lazu API の raw OpenAPI schema explorer。",
      markdown: `# OpenAPI Explorer

Lazu API の raw OpenAPI schema explorer。Primary docs flow uses the endpoint guide pages.

- OpenAPI JSON: /openapi.json
- OpenAPI YAML: /openapi.yaml`,
      title: "OpenAPI Explorer",
    },
  };
  const url = localizedUrlFor("/api-reference", locale);

  return {
    ...localized[locale],
    locale,
    routeKey: "/api-reference",
    url,
  };
}

async function listMdxFiles(directory: string, prefix = ""): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map(async (entry) => {
      const relativePath = path.join(prefix, entry.name);
      const absolutePath = path.join(directory, entry.name);

      if (entry.isDirectory()) {
        return listMdxFiles(absolutePath, relativePath);
      }

      return entry.isFile() && entry.name.endsWith(".mdx")
        ? [relativePath]
        : [];
    }),
  );

  return files.flat();
}

function pageUrlFor(file: string): string {
  const parts = file.split(path.sep);
  const filename = parts.pop() || "";
  const slug = path.basename(filename, ".mdx");
  const pathParts = slug === "index" ? parts : [...parts, slug];

  if (pathParts.length === 0) return "/";
  const route = `/${pathParts.join("/")}`;
  return slug === "index" ? `${route}/` : route;
}

function localeForUrl(url: string): LocaleCode {
  if (url === "/zh-TW" || url.startsWith("/zh-TW/")) return "zh-TW";
  if (url === "/zh" || url.startsWith("/zh/")) return "zh";
  if (url === "/ja" || url.startsWith("/ja/")) return "ja";
  return "en";
}

function routeKeyForUrl(url: string): string {
  const locale = localeForUrl(url);
  const prefix = localePrefixes[locale];
  if (!prefix) return url === "" ? "/" : url;
  const stripped = url.slice(prefix.length);
  return stripped === "" || stripped === "/" ? "/" : stripped;
}

function localizedUrlFor(routeKey: string, locale: LocaleCode): string {
  const prefix = localePrefixes[locale];
  if (routeKey === "/") return prefix ? `${prefix}/` : "/";
  return `${prefix}${routeKey}`;
}

type RouteGroup = {
  pagesByLocale: Map<LocaleCode, DocsPage>;
  routeKey: string;
};

function routeSortKey(routeKey: string): string {
  const order: Record<string, string> = {
    "/": "0",
    "/examples": "1",
    "/api-reference": "2",
    "/changelog": "3",
  };
  return `${order[routeKey] || "9"}:${routeKey}`;
}

function comparePages(left: DocsPage, right: DocsPage): number {
  const route = routeSortKey(left.routeKey).localeCompare(
    routeSortKey(right.routeKey),
  );
  if (route !== 0) return route;

  return LOCALES.indexOf(left.locale) - LOCALES.indexOf(right.locale);
}

function buildRouteGroups(pages: DocsPage[]): RouteGroup[] {
  const groups = new Map<string, RouteGroup>();
  for (const page of pages) {
    const group =
      groups.get(page.routeKey) ||
      ({
        pagesByLocale: new Map<LocaleCode, DocsPage>(),
        routeKey: page.routeKey,
      } satisfies RouteGroup);
    group.pagesByLocale.set(page.locale, page);
    groups.set(page.routeKey, group);
  }

  return [...groups.values()].sort((left, right) =>
    routeSortKey(left.routeKey).localeCompare(routeSortKey(right.routeKey)),
  );
}

function renderAlternateLinks(group: RouteGroup): string {
  const alternates = LOCALES.flatMap((locale) => {
    const page = group.pagesByLocale.get(locale);
    if (!page) return [];
    return `    <xhtml:link rel="alternate" hreflang="${locale}" href="${escapeXml(absoluteUrl(page.url))}" />`;
  });
  const defaultPage =
    group.pagesByLocale.get("en") || group.pagesByLocale.values().next().value;
  if (defaultPage) {
    alternates.push(
      `    <xhtml:link rel="alternate" hreflang="x-default" href="${escapeXml(absoluteUrl(defaultPage.url))}" />`,
    );
  }
  return alternates.join("\n");
}

function renderSitemap(pages: DocsPage[]): string {
  // No <lastmod>: every page is rebuilt on every deploy, so the build date
  // would claim they all changed each time, and search engines stop trusting
  // lastmod on sites where it is wrong. The image has no git history to read
  // real dates from.
  const groups = buildRouteGroups(pages);
  const entries = groups.flatMap((group) =>
    LOCALES.flatMap((locale) => {
      const page = group.pagesByLocale.get(locale);
      if (!page) return [];
      const priority = page.routeKey === "/" ? "1.0" : "0.8";

      return `  <url>
    <loc>${escapeXml(absoluteUrl(page.url))}</loc>
${renderAlternateLinks(group)}
    <changefreq>weekly</changefreq>
    <priority>${priority}</priority>
  </url>`;
    }),
  );

  return `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">
${entries.join("\n")}
</urlset>
`;
}

async function syncGeneratedTextAssets(): Promise<void> {
  const pages = (await loadPages()).sort(comparePages);
  const lines = pages.map(
    (page) =>
      `- [${page.title}](${absoluteUrl(page.url)}): ${page.description}`,
  );
  const index = `# Lazu Docs

Lazu is a unified AI model gateway for people and agents.

Recommended workflow:
- Use Authorization: Bearer <api-key>
- Call GET /api/models/catalog first
- Choose the model by modality, supported_endpoint_types, supported_endpoints, pricing, and default_endpoint_type
- Then call the matching Lazu endpoint

Important URLs:
- Docs home: ${absoluteUrl("/")}
- API Explorer: ${absoluteUrl("/api-reference")}
- OpenAPI JSON: ${absoluteUrl("/openapi.json")}
- Full LLM context: ${absoluteUrl("/llms-full.txt")}

## Documentation pages

${lines.join("\n")}
`;
  // Serialized pages already open with their own H1 (EndpointPage emits it,
  // prose pages author it); only synthesize one when it is missing.
  const full = `${pages
    .map((page) =>
      page.markdown.startsWith("# ")
        ? page.markdown
        : `# ${page.title}\n\n${page.markdown}`,
    )
    .join("\n\n---\n\n")}\n`;
  const sitemap = renderSitemap(pages);

  await mkdir(publicDir, { recursive: true });

  // One raw-Markdown sibling per route: /endpoints/chat -> /endpoints/chat.md.
  // The docs "复制为 Markdown" button and the ChatGPT/Claude deep links fetch
  // these; they are what an agent reads instead of the SPA shell.
  await Promise.all(
    pages.map(async (page) => {
      const mdPath =
        page.url === "/" || page.url.endsWith("/")
          ? `${page.url}index.md`
          : `${page.url}.md`;
      const target = path.join(publicDir, ...mdPath.split("/").filter(Boolean));
      await mkdir(path.dirname(target), { recursive: true });
      // Front matter so a pasted copy still says what it is and where it came
      // from; JSON strings are valid YAML scalars, so values need no escaping.
      const header = [
        "---",
        `title: ${JSON.stringify(page.title)}`,
        ...(page.description
          ? [`description: ${JSON.stringify(page.description)}`]
          : []),
        `source: ${absoluteUrl(page.url)}`,
        `updated: ${new Date().toISOString().slice(0, 10)}`,
        "---",
      ].join("\n");
      const body = page.markdown.startsWith("# ")
        ? page.markdown
        : `# ${page.title}\n\n${page.markdown}`;
      await writeFile(target, `${header}\n\n${body}\n`, "utf8");
    }),
  );

  await Promise.all([
    writeFile(path.join(publicDir, "llms.txt"), index, "utf8"),
    writeFile(path.join(publicDir, "llms-full.txt"), full, "utf8"),
    writeFile(path.join(publicDir, "sitemap.xml"), sitemap, "utf8"),
    writeFile(
      path.join(publicDir, "robots.txt"),
      // Served at /docs/robots.txt, which crawlers never read: lazu.ai's
      // robots.txt belongs to the web app and points at this sitemap. Kept so
      // the docs container is self-describing when run on its own.
      // The wildcard already allows everything; naming the AI crawlers makes
      // the welcome explicit and survives any future tightening of `*`.
      [
        "User-agent: *",
        "Allow: /",
        "",
        ...[
          "GPTBot",
          "OAI-SearchBot",
          "ChatGPT-User",
          "ClaudeBot",
          "Claude-Web",
          "anthropic-ai",
          "PerplexityBot",
          "Perplexity-User",
          "Google-Extended",
          "Applebot-Extended",
          "meta-externalagent",
          "CCBot",
          "cohere-ai",
        ].flatMap((bot) => [`User-agent: ${bot}`, "Allow: /", ""]),
        `Sitemap: ${absoluteUrl("/sitemap.xml")}`,
        "",
      ].join("\n"),
      "utf8",
    ),
  ]);

  console.log("[docs] synced LLM, sitemap, and robots assets");
}

await syncOpenApi();
await syncGeneratedTextAssets();
