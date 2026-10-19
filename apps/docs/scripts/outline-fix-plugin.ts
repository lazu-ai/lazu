// Fix a crash in Vocs 1.4's outline (the "On this page" column).
//
// When the bottom-of-page marker scrolls out of view while the last outline
// item is active, Vocs activates the item before it with
// `items[items.length - 2].id`. A page with fewer than two headings (the docs
// home) has no such item, so the observer throws "Cannot read properties of
// undefined (reading 'id')". The site footer under every page is what now
// scrolls that marker away. Read the id optionally instead.
//
// The rewrite throws when its target text is gone, so a Vocs upgrade that
// changes the file fails the build rather than silently dropping the fix.
import type { Plugin } from "vite";

const file = /[\\/]vocs[\\/]_lib[\\/]app[\\/]components[\\/]Outline\.js$/;
const from = "setActiveId(items[items.length - 2].id);";
const to = "setActiveId(items[items.length - 2]?.id);";

export function outlineFix(): Plugin {
  return {
    name: "lazu-docs-outline-fix",
    enforce: "pre",
    transform(code, id) {
      if (!file.test(id)) return null;
      if (!code.includes(from)) {
        throw new Error(
          `[docs] outline fix: ${id} changed upstream; update scripts/outline-fix-plugin.ts`,
        );
      }
      return code.replace(from, to);
    },
  };
}
