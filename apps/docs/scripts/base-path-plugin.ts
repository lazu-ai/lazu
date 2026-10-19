// Fix the spots where Vocs 1.4 ignores its own `basePath`.
//
// The docs live under lazu.ai/docs. Vocs prefixes its routes, bundles and
// router links with basePath, but three places still assume the site sits at
// an origin root:
//
//   1. Prerender hands <StaticRouter basename={basePath}> the bare route
//      ("/quickstart") as its location. React Router refuses a location
//      outside the basename, so every page prerendered empty. Prefix it
//      where the router is built; the route lookup keeps the bare path.
//   2. The skip link is a plain <a> built from the router's pathname, which
//      is basename-stripped ("/quickstart#vocs-content" would leave the
//      docs). A same-page fragment is all it needs.
//   3. The HTML template's "Are you an LLM?" line links /llms.txt and
//      /llms-full.txt at the origin root.
//
// Each rewrite throws when its target text is gone, so a Vocs upgrade that
// changes these files fails the build instead of shipping broken pages.
import type { Plugin } from "vite";

const rewrites: Array<{ file: RegExp; from: string; to: string }> = [
  {
    file: /[\\/]vocs[\\/]_lib[\\/]app[\\/]index\.server\.js$/,
    from: "_jsx(StaticRouter, { location: location, basename: basePath,",
    to: "_jsx(StaticRouter, { location: basePath && location !== basePath && !location.startsWith(basePath + '/') ? basePath + location : location, basename: basePath,",
  },
  {
    file: /[\\/]vocs[\\/]_lib[\\/]app[\\/]components[\\/]SkipLink\.js$/,
    from: "href: `${pathname}#${skipLinkId}`",
    to: "href: `#${skipLinkId}`",
  },
];

export function basePathFixes(basePath: string): Plugin {
  return {
    name: "lazu-docs-base-path",
    enforce: "pre",
    transform(code, id) {
      const rewrite = rewrites.find((entry) => entry.file.test(id));
      if (!rewrite) return null;
      if (!code.includes(rewrite.from)) {
        throw new Error(
          `[docs] base path: ${id} changed upstream; update scripts/base-path-plugin.ts`,
        );
      }
      return code.replace(rewrite.from, rewrite.to);
    },
    transformIndexHtml(html) {
      return html.replace(
        /href="\/(llms(?:-full)?\.txt)"/g,
        `href="${basePath}/$1"`,
      );
    },
  };
}
