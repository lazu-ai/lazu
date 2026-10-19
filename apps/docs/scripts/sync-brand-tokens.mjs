import { access, copyFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { relative, resolve } from "node:path";
import process from "node:process";

const docsRoot = resolve(import.meta.dirname, "..");
const repoRoot = resolve(docsRoot, "../..");
const sources = ["colors.css", "data-viz.css", "ui.css", "public-web.css"].map(
  (file) => resolve(repoRoot, "brand/tokens", file),
);
const output = resolve(docsRoot, "public/brand.generated.css");
const publicFonts = resolve(docsRoot, "public/fonts");
const fonts = [
  {
    source: resolve(
      docsRoot,
      "node_modules/@fontsource-variable/geist/files/geist-latin-wght-normal.woff2",
    ),
    output: "geist-latin-wght-normal.woff2",
  },
  ...[400, 500, 600].map((weight) => ({
    source: resolve(
      docsRoot,
      `node_modules/@fontsource/ibm-plex-mono/files/ibm-plex-mono-latin-${weight}-normal.woff2`,
    ),
    output: `ibm-plex-mono-latin-${weight}-normal.woff2`,
  })),
  // Display serif (Seal, LOCKED 2026-09-20). The web app ships the latin
  // subset; brand keeps that exact file so docs titles never fall back to Georgia.
  {
    source: resolve(
      repoRoot,
      "brand/assets/fonts/instrument-serif-latin-400.woff2",
    ),
    output: "instrument-serif-latin-400.woff2",
  },
];
const chunks = await Promise.all(sources.map((file) => readFile(file, "utf8")));
// Font URLs stay relative: they resolve against this stylesheet's own URL
// (/docs/brand.generated.css), so the docs work under the /docs base path.
const generated = [
  "/* GENERATED from brand/tokens. Run npm run brand:sync; do not edit. */",
  '@font-face{font-family:"Geist";font-style:normal;font-display:swap;font-weight:100 900;src:url("fonts/geist-latin-wght-normal.woff2") format("woff2-variations")}',
  ...[400, 500, 600].map(
    (weight) =>
      `@font-face{font-family:"IBM Plex Mono";font-style:normal;font-display:swap;font-weight:${weight};src:url("fonts/ibm-plex-mono-latin-${weight}-normal.woff2") format("woff2")}`,
  ),
  '@font-face{font-family:"Instrument Serif";font-style:normal;font-display:swap;font-weight:400;src:url("fonts/instrument-serif-latin-400.woff2") format("woff2");unicode-range:U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+2074,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD}',
  ...chunks.map(
    (chunk, index) =>
      `\n/* source: ${relative(repoRoot, sources[index])} */\n${chunk.trim()}\n`,
  ),
].join("\n");

if (process.argv.includes("--check")) {
  const current = await readFile(output, "utf8").catch(() => "");
  if (current !== generated) {
    console.error(
      `Brand token drift: ${relative(repoRoot, output)}. Run \`cd apps/docs && npm run brand:sync\` and commit the result.`,
    );
    process.exit(1);
  }
  await Promise.all(
    fonts.map((font) => access(resolve(publicFonts, font.output))),
  );
  console.log("Docs Brand token check passed.");
} else {
  await mkdir(publicFonts, { recursive: true });
  await Promise.all(
    fonts.map((font) =>
      copyFile(font.source, resolve(publicFonts, font.output)),
    ),
  );
  await writeFile(output, generated);
  console.log(`Synced Brand tokens to ${relative(repoRoot, output)}.`);
}
