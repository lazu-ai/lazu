// Write dist/404.html: the page nginx serves, with a real 404 status, for any
// address that is not a prerendered docs page.
//
// Vocs prerenders every docs page but no 404, and nginx used to fall back to
// the home page's HTML — so a wrong link answered 200 with the home page and
// only swapped to "not found" after hydration. The 404 shell is the home
// page's document (same head, same asset bundle) with:
//   - the prerendered home content taken out of #app, so nothing but the
//     docs 404 (components/not-found.tsx) ever paints there;
//   - its own title and noindex, and none of the home page's canonical,
//     hreflang, og:url or JSON-LD, which would claim this URL is "/".
// Runs after postbuild-geo, which adds those tags.
import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const distDir = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../dist",
);

const home = await readFile(path.join(distDir, "index.html"), "utf8");

const open = '<div id="app">';
const start = home.indexOf(open);
const end = home.lastIndexOf("</body>");
if (start < 0 || end < start) {
  throw new Error("[docs] postbuild 404: #app not found in dist/index.html");
}
const appClose = home.lastIndexOf("</div>", end);

let page =
  home.slice(0, start + open.length) + home.slice(appClose, home.length);

page = page
  .replace(/<link rel="canonical"[^>]*>\s*/g, "")
  .replace(/<link rel="alternate"[^>]*>\s*/g, "")
  .replace(/<meta property="og:url"[^>]*>\s*/g, "")
  .replace(/<script type="application\/ld\+json">[\s\S]*?<\/script>\s*/g, "")
  .replace(
    /<title>[^<]*<\/title>/,
    '<title>Page not found – Lazu Docs</title><meta name="robots" content="noindex"/>',
  );

await writeFile(path.join(distDir, "404.html"), page, "utf8");
console.log("[docs] postbuild 404: wrote dist/404.html");
