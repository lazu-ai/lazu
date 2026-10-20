import { createElement, Fragment } from "react";
import { defineConfig } from "vocs";
import { createSidebars } from "./components/docs-nav";
import { basePathFixes } from "./scripts/base-path-plugin";
import { searchPerformance } from "./scripts/search-performance-plugin";
import { outlineFix } from "./scripts/outline-fix-plugin";
import { notFoundPage } from "./scripts/not-found-plugin";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

// The docs live under lazu.ai/docs (a subfolder of the main domain, not a
// docs.* subdomain). Vocs prefixes its own routes, links and bundles with
// this; the raw <head> tags below and the injected scripts have to add it
// themselves.
const BASE_PATH = "/docs";

// Scripts a deployment adds to every page's <head>: each extensions/*.head.js
// file is inlined as one script, in name order. The public tree has none.
const extensionsDir = join(import.meta.dirname, "extensions");
const extensionHeadScripts = existsSync(extensionsDir)
  ? readdirSync(extensionsDir)
      .filter((name) => name.endsWith(".head.js"))
      .sort()
      .map((name) => ({
        key: name,
        source: readFileSync(join(extensionsDir, name), "utf8"),
      }))
  : [];

// Brand is the only visual source. Vocs maps its required theme keys onto the
// generated Kumo semantic tokens loaded from /brand.generated.css.
const lightTokens = {
  background: "var(--lazu-page)",
  background2: "var(--lazu-surface)",
  background3: "var(--lazu-surface)",
  background4: "var(--lazu-surface-muted)",
  background5: "var(--lazu-surface-raised)",
  backgroundAccent: "var(--lazu-action)",
  backgroundAccentHover: "var(--lazu-action-hover)",
  backgroundAccentText: "var(--lazu-on-action)",
  // Vocs uses `backgroundDark` for CHROME (mobile top nav, sidebar scroll
  // curtain), not only for code. Pointing it at --lazu-code-bg painted a
  // graphite bar behind ink-coloured text on every viewport <= 1080px
  // (1.00:1 contrast) and dropped a dark gradient into the light sidebar.
  // Code blocks have their own token (`codeBlockBackground`) below, so this
  // one belongs on paper.
  backgroundDark: "var(--lazu-page)",
  backgroundDarkTint: "var(--lazu-surface-muted)",
  border: "var(--lazu-border)",
  border2: "var(--lazu-border-strong)",
  borderAccent: "var(--lazu-action)",
  heading: "var(--lazu-ink)",
  text: "var(--lazu-ink)",
  text2: "var(--lazu-text-muted)",
  text3: "var(--lazu-text-faint)",
  text4: "var(--lazu-text-faint)",
  textAccent: "var(--lazu-action)",
  textAccentHover: "var(--lazu-action-hover)",
  title: "var(--lazu-ink)",
  shadow: "transparent",
  shadow2: "var(--lazu-shadow-overlay)",
  codeBlockBackground: "var(--lazu-code-bg)",
  codeInlineBackground: "var(--lazu-surface-muted)",
  codeInlineText: "var(--lazu-ink)",
  codeInlineBorder: "var(--lazu-border)",
} as const;

const darkTokens = lightTokens;

// Vocs expects each color token to declare both light + dark values when
// `colorScheme: "system"` is on. Pair them up token by token from the
// matching dictionaries above so we can keep editing them as flat lists.
const colorTokens = Object.fromEntries(
  (Object.keys(lightTokens) as Array<keyof typeof lightTokens>).map((key) => [
    key,
    { light: lightTokens[key], dark: darkTokens[key] },
  ]),
);

// Build the head fragment with React.createElement so this file stays .ts
// (no JSX). Wrap as a function — when Vocs sees a plain ReactElement here
// it falls into the `typeof === "object"` branch and treats it as a
// path-keyed dictionary instead of an element, so the children never make
// it into <head>. Returning the element from a function dodges that and
// hits Vocs' function-form path.
const head = () =>
  createElement(
    Fragment,
    null,
    ...extensionHeadScripts.map((script) =>
      createElement("script", {
        key: script.key,
        dangerouslySetInnerHTML: { __html: script.source },
      }),
    ),
    createElement("link", {
      key: "brand-theme",
      rel: "stylesheet",
      href: `${BASE_PATH}/brand.generated.css`,
    }),
    createElement("link", {
      key: "docs-theme",
      rel: "stylesheet",
      href: `${BASE_PATH}/styles.css`,
    }),
    // Vanilla JS that mounts a theme toggle + language switcher into the
    // bottom of Vocs' sidebar (next to the GitHub icon). Vocs doesn't
    // expose a plug point there in config, so we DOM-inject it at
    // runtime. Plain JS so we don't pull React onto every page.
    createElement("script", {
      key: "docs-sidebar-extras",
      src: `${BASE_PATH}/sidebar-extras.js`,
      defer: true,
    }),
    // The "give this page to a model" row under each title: copy the route's
    // .md sibling, or open ChatGPT / Claude with a prompt pointed at it.
    // The site-wide footer (columns + the operator's legal line) under
    // every page; Vocs has no slot for it.
    createElement("script", {
      key: "docs-site-footer",
      src: `${BASE_PATH}/site-footer.js`,
      defer: true,
    }),
    createElement("script", {
      key: "docs-agent-actions",
      src: `${BASE_PATH}/agent-actions.js`,
      defer: true,
    }),
  );

const codeToken = (scope: string | string[], token: string) => ({
  scope,
  settings: { foreground: `var(--lazu-code-${token})` },
});

const inkCodeTheme = {
  name: "lazu-ink",
  type: "dark" as const,
  colors: {
    "editor.background": "var(--lazu-code-bg)",
    "editor.foreground": "var(--lazu-code-text)",
  },
  fg: "var(--lazu-code-text)",
  bg: "var(--lazu-code-bg)",
  tokenColors: [
    codeToken(["comment", "punctuation.definition.comment"], "muted"),
    codeToken(
      ["string", "string.quoted", "punctuation.definition.string"],
      "string",
    ),
    codeToken(
      [
        "keyword",
        "storage",
        "support.function",
        "entity.name.function",
        "support.type.property-name",
        "entity.name.tag",
        "meta.object-literal.key",
      ],
      "keyword",
    ),
    codeToken(
      [
        "constant.numeric",
        "constant.language",
        "constant.other.option",
        "variable.parameter",
      ],
      "flag",
    ),
  ],
};

export default defineConfig({
  vite: {
    plugins: [
      notFoundPage(),
      basePathFixes(BASE_PATH),
      outlineFix(),
      searchPerformance(),
    ],
  },
  description:
    "Lazu API documentation for authentication, model discovery, OpenAI-compatible calls, native provider APIs and production troubleshooting.",
  // The page actions (Copy page, split button) replace Vocs' own AI dropdown.
  aiCta: false,
  basePath: BASE_PATH,
  head,
  iconUrl: "/favicon.svg",
  // Top-left brand mark replaces the "Lazu Docs" text in the sidebar
  // logo slot. Two variants so the wordmark stays readable in both themes.
  logoUrl: {
    light: "/logo.svg",
    dark: "/logo-dark.svg",
  },
  // Code is dark ink on warm paper in both themes (Docs B workbench, LOCKED
  // 2026-09-23). The theme paints with Brand's code tokens as CSS variables,
  // so there is no second copy of the palette here and dark mode only has to
  // swap the variables.
  markdown: {
    code: {
      themes: { light: inkCodeTheme, dark: inkCodeTheme },
      // The dark header row of each block prints its language.
      transformers: [
        {
          name: "lazu-code-language",
          pre(node) {
            node.properties["data-language"] = this.options.lang;
          },
        },
      ],
    },
  },
  rootDir: ".",
  search: {},
  // Per-locale sidebar. Vocs matches the longest URL prefix first, so
  // /zh-TW/ paths fall to the zh-TW block, /zh/ to zh, /ja/ to ja, and
  // everything else (the default English route) to /.
  //
  // All four locales now ship index, examples, the OpenAPI Explorer, and
  // changelog routes. Keep the sidebar links locale-local so the
  // language switcher does not have to fall back for these docs pages.
  //
  // Language switcher lives in the sidebar footer next to GitHub
  // (apps/docs/public/sidebar-extras.js) so we don't duplicate a "中文"
  // entry here or in topNav.
  sidebar: createSidebars(),
  socials: [],
  theme: {
    accentColor: { light: "#1f6f6b", dark: "#4fb3ad" },
    colorScheme: "system",
    variables: {
      color: colorTokens,
      fontFamily: {
        default:
          '"Geist", -apple-system, BlinkMacSystemFont, "PingFang SC", sans-serif',
        mono: '"IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      },
    },
  },
  title: "Lazu API Docs",
  topNav: [
    { link: "/quickstart", text: "Guides" },
    { link: "/endpoints/chat", text: "API reference" },
    { link: "/changelog", text: "Changelog" },
    // The model pages live on the main site, outside /docs. An absolute URL
    // keeps Vocs from prefixing it; sidebar-extras.js turns it into a
    // same-tab link to /models, /zh/models, ... for the active locale.
    { link: "https://lazu.ai/models", text: "Models" },
  ],
});
