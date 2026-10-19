// Swap Vocs' built-in catch-all page for the Lazu docs 404
// (components/not-found.tsx). Vocs hard-codes its NotFound into the `*`
// route and offers no config for it, so the swap happens at module
// resolution: when Vocs' routes module asks for its own NotFound, hand it
// ours. Applies to the dev server, the client bundle and the prerender.
import path from "node:path";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";

const replacement = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../components/not-found.tsx",
);
const vocsRoutes = /[\\/]vocs[\\/]_lib[\\/]app[\\/]routes\.js$/;

export function notFoundPage(): Plugin {
  return {
    name: "lazu-docs-not-found",
    enforce: "pre",
    resolveId(source, importer) {
      if (
        source === "./components/NotFound.js" &&
        importer &&
        vocsRoutes.test(importer)
      ) {
        return replacement;
      }
      return null;
    },
  };
}
