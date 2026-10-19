import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, "../../..");
const currentDir = resolve(scriptDir, "current");
const rasterDir = resolve(scriptDir, "raster");
const socialDir = resolve(scriptDir, "social");
const webBrandDir = resolve(repoRoot, "apps/web/src/brand/assets");
const webPublicDir = resolve(repoRoot, "apps/web/public");
const webFullDir = resolve(repoRoot, "apps/web/assets/logo/full");
const docsPublicDir = resolve(repoRoot, "apps/docs/public");
const packageSvgDir = resolve(repoRoot, "apps/lazu-logo-package/svg");
const packagePngDir = resolve(repoRoot, "apps/lazu-logo-package/png");

const COLORS = {
  graphite: "#20201d",
  paper: "#f8f8f6",
};

const ICON_SIZES = [16, 32, 48, 64, 128, 150, 180, 192, 256, 512, 1024];
const REQUIRED_NATIVE_TOOLS = ["rsvg-convert", "magick"];

const assertNativeTools = () => {
  const missing = [];
  for (const command of REQUIRED_NATIVE_TOOLS) {
    try {
      execFileSync(command, ["--version"], { stdio: "ignore" });
    } catch {
      missing.push(command);
    }
  }
  if (missing.length > 0) {
    throw new Error(
      `Missing native logo-generation dependencies: ${missing.join(", ")}. Install librsvg and ImageMagick 7, then verify \`rsvg-convert --version\` and \`magick --version\`. No assets were changed.`,
    );
  }
};

// Fail before the first write so a missing native renderer can never leave
// fresh SVGs beside stale raster and ICO exports.
assertNativeTools();

const ensureDir = (path) => mkdirSync(path, { recursive: true });
const write = (path, content) => {
  ensureDir(dirname(path));
  writeFileSync(path, `${content.trim().replace(/[ \t]+$/gm, "")}\n`, "utf8");
};
const copy = (from, to) => {
  ensureDir(dirname(to));
  copyFileSync(from, to);
};

// Seal mark, LOCKED 2026-09-20. Drawn on the 64-unit grid: a 10-unit L sunk to
// the lower left and a single dot in the upper right, both cut out of a
// circular container. The 1024 icon canvas scales the grid by 16.
const sealGlyph = (fill, scale = 16, offset = 0) => `
  <g transform="translate(${offset} ${offset}) scale(${scale})" fill="${fill}">
    <path d="M18 17h10v20h18v10H18z"></path>
    <circle cx="41" cy="23" r="5"></circle>
  </g>`;

const container = ({ background, border = false }) => `
  <circle cx="512" cy="512" r="512" fill="${background}"></circle>
  ${border ? `<circle cx="512" cy="512" r="510.5" fill="none" stroke="${COLORS.graphite}" stroke-opacity=".1" stroke-width="3"></circle>` : ""}`;

const iconSvg = ({ background, glyph, border = false }) => `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" role="img" aria-label="Lazu">
  ${container({ background, border })}
  ${sealGlyph(glyph)}
</svg>`;

// Single-colour seal for print, embossing and etching: the container becomes
// a ring so the mark still reads as a stamp without a second colour.
const flatIconSvg = (fill) => `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" role="img" aria-label="Lazu">
  <circle cx="32" cy="32" r="30.5" fill="none" stroke="${fill}" stroke-width="3"></circle>
  ${sealGlyph(fill, 1)}
</svg>`;

// Instrument Serif Regular outlines for "Lazu" (LOCKED 2026-09-20), shaped once
// by extract-wordmark.py and frozen in wordmark-outlines.json so every export
// is independent of host fonts. The wordmark is set at a 64-unit em on the
// 80-unit primary canvas: cap height 46, so the L stands as tall as the seal's
// own L and the baseline sits at y=63.
const OUTLINES = JSON.parse(
  readFileSync(resolve(scriptDir, "wordmark-outlines.json"), "utf8"),
);
const WORDMARK_EM = 62;
const WORDMARK_SCALE = WORDMARK_EM / OUTLINES.unitsPerEm;
const WORDMARK_ADVANCE = OUTLINES.advance * WORDMARK_SCALE;
const WORDMARK_CAP = OUTLINES.capHeight * WORDMARK_SCALE;
// Visible bounds of the shaped word on the canvas, y measured from the
// baseline (negative = above), taken from the outlines rather than the advance
// so a serif that overhangs its own advance is never clipped.
const WORDMARK_BOUNDS = {
  minX: OUTLINES.bounds.minX * WORDMARK_SCALE,
  minY: -OUTLINES.bounds.maxY * WORDMARK_SCALE,
  maxX: OUTLINES.bounds.maxX * WORDMARK_SCALE,
  maxY: -OUTLINES.bounds.minY * WORDMARK_SCALE,
};
const WORDMARK_CLEARSPACE = 15;
const WORDMARK_VIEWBOX_WIDTH =
  WORDMARK_BOUNDS.maxX - WORDMARK_BOUNDS.minX + WORDMARK_CLEARSPACE * 2;
const WORDMARK_VIEWBOX_HEIGHT =
  WORDMARK_BOUNDS.maxY - WORDMARK_BOUNDS.minY + WORDMARK_CLEARSPACE * 2;
const PRIMARY_CLEARSPACE = 15;
const PRIMARY_GAP = 18;
const PRIMARY_WORDMARK_X = 80 + PRIMARY_GAP;
const PRIMARY_BASELINE = 63;
// The canvas starts at x = -15, so the width carries both clearspaces.
const PRIMARY_VIEWBOX_WIDTH = Math.ceil(
  PRIMARY_CLEARSPACE +
    PRIMARY_WORDMARK_X +
    WORDMARK_BOUNDS.maxX +
    PRIMARY_CLEARSPACE,
);

// Font outlines are y-up; the canvas is y-down, so the group flips the y axis.
const wordmarkOutline = ({ fill, x = 0, y = 0, scale = 1 }) => `
  <g transform="translate(${x} ${y}) scale(${scale * WORDMARK_SCALE} ${-scale * WORDMARK_SCALE})" fill="${fill}">
    ${OUTLINES.glyphs
      .map(
        (glyph) =>
          `<path transform="translate(${glyph.x} 0)" d="${glyph.d}"></path>`,
      )
      .join("\n    ")}
  </g>`;

const wordmarkSvg = (fill) => `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${WORDMARK_VIEWBOX_WIDTH} ${WORDMARK_VIEWBOX_HEIGHT}" role="img" aria-label="Lazu">
  ${wordmarkOutline({
    fill,
    x: WORDMARK_CLEARSPACE - WORDMARK_BOUNDS.minX,
    y: WORDMARK_CLEARSPACE - WORDMARK_BOUNDS.minY,
  })}
</svg>`;

const lockupIcon = ({
  background,
  glyph,
  border = false,
  x = 0,
  y = 0,
  size = 80,
}) => `
  <svg x="${x}" y="${y}" width="${size}" height="${size}" viewBox="0 0 1024 1024">
    ${container({ background, border })}
    ${sealGlyph(glyph)}
  </svg>`;

const primarySvg = ({ background, glyph, wordmark, border = false }) => `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="-${PRIMARY_CLEARSPACE} 0 ${PRIMARY_VIEWBOX_WIDTH} 80" role="img" aria-label="Lazu">
  ${lockupIcon({ background, glyph, border })}
  ${wordmarkOutline({ fill: wordmark, x: PRIMARY_WORDMARK_X, y: PRIMARY_BASELINE })}
</svg>`;

const stackedSvg = ({ background, glyph, wordmark, border = false }) => `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 248" role="img" aria-label="Lazu">
  ${lockupIcon({ background, glyph, border, x: 70, y: 10, size: 100 })}
  ${wordmarkOutline({
    fill: wordmark,
    x: (240 - (WORDMARK_BOUNDS.minX + WORDMARK_BOUNDS.maxX)) / 2,
    y: 218,
  })}
</svg>`;

const adaptiveFaviconSvg = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" role="img" aria-label="Lazu">
  <style>
    .container { fill: ${COLORS.graphite}; }
    .glyph { fill: ${COLORS.paper}; }
    @media (prefers-color-scheme: dark) {
      .container { fill: ${COLORS.paper}; }
      .glyph { fill: ${COLORS.graphite}; }
    }
  </style>
  <circle class="container" cx="512" cy="512" r="512"></circle>
  <g class="glyph" transform="scale(16)">
    <path d="M18 17h10v20h18v10H18z"></path>
    <circle cx="41" cy="23" r="5"></circle>
  </g>
</svg>`;

// Apple applies its own rounded app-icon mask, so the source must be opaque
// edge to edge instead of reusing the transparent-corner browser icon.
const appleTouchIconSvg = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" role="img" aria-label="Lazu">
  <rect width="1024" height="1024" fill="${COLORS.graphite}"></rect>
  ${sealGlyph(COLORS.paper)}
</svg>`;

const variants = {
  graphite: {
    icon: iconSvg({ background: COLORS.graphite, glyph: COLORS.paper }),
    primary: primarySvg({
      background: COLORS.graphite,
      glyph: COLORS.paper,
      wordmark: COLORS.graphite,
    }),
    stacked: stackedSvg({
      background: COLORS.graphite,
      glyph: COLORS.paper,
      wordmark: COLORS.graphite,
    }),
    wordmark: wordmarkSvg(COLORS.graphite),
  },
  paper: {
    icon: iconSvg({
      background: COLORS.paper,
      glyph: COLORS.graphite,
      border: true,
    }),
    primary: primarySvg({
      background: COLORS.paper,
      glyph: COLORS.graphite,
      wordmark: COLORS.paper,
      border: true,
    }),
    stacked: stackedSvg({
      background: COLORS.paper,
      glyph: COLORS.graphite,
      wordmark: COLORS.paper,
      border: true,
    }),
    wordmark: wordmarkSvg(COLORS.paper),
  },
};

for (const [tone, assets] of Object.entries(variants)) {
  for (const [kind, svg] of Object.entries(assets)) {
    write(resolve(currentDir, `lazu-${kind}-${tone}.svg`), svg);
  }
}

write(
  resolve(currentDir, "lazu-icon-flat-black.svg"),
  flatIconSvg(COLORS.graphite),
);
write(
  resolve(currentDir, "lazu-icon-flat-white.svg"),
  flatIconSvg(COLORS.paper),
);
write(resolve(currentDir, "lazu-icon-black.svg"), variants.graphite.icon);
write(resolve(currentDir, "lazu-icon-white.svg"), variants.paper.icon);
write(resolve(currentDir, "lazu-primary-black.svg"), variants.graphite.primary);
write(resolve(currentDir, "lazu-primary-white.svg"), variants.paper.primary);
write(resolve(currentDir, "lazu-stacked-black.svg"), variants.graphite.stacked);
write(resolve(currentDir, "lazu-stacked-white.svg"), variants.paper.stacked);
write(
  resolve(currentDir, "lazu-wordmark-black.svg"),
  variants.graphite.wordmark,
);
write(resolve(currentDir, "lazu-wordmark-white.svg"), variants.paper.wordmark);
write(resolve(currentDir, "lazu-apple-touch-icon.svg"), appleTouchIconSvg);
write(resolve(scriptDir, "favicon.svg"), adaptiveFaviconSvg);

const webAssetCopies = [
  ["lazu-icon-graphite.svg", variants.graphite.icon],
  ["lazu-icon-paper.svg", variants.paper.icon],
  ["lazu-primary-graphite.svg", variants.graphite.primary],
  ["lazu-primary-paper.svg", variants.paper.primary],
  ["lazu-stacked-graphite.svg", variants.graphite.stacked],
  ["lazu-stacked-paper.svg", variants.paper.stacked],
  ["lazu-wordmark-graphite.svg", variants.graphite.wordmark],
  ["lazu-wordmark-paper.svg", variants.paper.wordmark],
  ["lazu-icon-black.svg", variants.graphite.icon],
  ["lazu-icon-white.svg", variants.paper.icon],
  ["lazu-primary-black.svg", variants.graphite.primary],
  ["lazu-primary-white.svg", variants.paper.primary],
  ["lazu-stacked-black.svg", variants.graphite.stacked],
  ["lazu-stacked-white.svg", variants.paper.stacked],
  ["lazu-wordmark-black.svg", variants.graphite.wordmark],
  ["lazu-wordmark-white.svg", variants.paper.wordmark],
];

for (const [name, svg] of webAssetCopies) {
  write(resolve(webBrandDir, name), svg);
}

write(resolve(webPublicDir, "favicon.svg"), adaptiveFaviconSvg);
write(resolve(webPublicDir, "favicon-graphite.svg"), variants.graphite.icon);
write(resolve(webPublicDir, "favicon-paper.svg"), variants.paper.icon);
write(resolve(webPublicDir, "lazu-icon-graphite.svg"), variants.graphite.icon);
write(resolve(webPublicDir, "lazu-icon-paper.svg"), variants.paper.icon);
write(
  resolve(webPublicDir, "lazu-logo-graphite.svg"),
  variants.graphite.primary,
);
write(resolve(webPublicDir, "lazu-logo-paper.svg"), variants.paper.primary);

write(resolve(docsPublicDir, "favicon.svg"), adaptiveFaviconSvg);
write(resolve(docsPublicDir, "icon-graphite.svg"), variants.graphite.icon);
write(resolve(docsPublicDir, "icon-paper.svg"), variants.paper.icon);
write(resolve(docsPublicDir, "logo.svg"), variants.graphite.primary);
write(resolve(docsPublicDir, "logo-dark.svg"), variants.paper.primary);

const packageSvgCopies = [
  ["lazu-icon-graphite.svg", variants.graphite.icon],
  ["lazu-icon-paper.svg", variants.paper.icon],
  ["lazu-primary-graphite.svg", variants.graphite.primary],
  ["lazu-primary-paper.svg", variants.paper.primary],
  ["lazu-stacked-graphite.svg", variants.graphite.stacked],
  ["lazu-stacked-paper.svg", variants.paper.stacked],
  ["lazu-wordmark-graphite.svg", variants.graphite.wordmark],
  ["lazu-wordmark-paper.svg", variants.paper.wordmark],
  ["lazu-apple-touch-icon.svg", appleTouchIconSvg],
  ["favicon.svg", adaptiveFaviconSvg],
];

for (const [name, svg] of packageSvgCopies) {
  write(resolve(packageSvgDir, name), svg);
}

const renderSvg = (input, output, width, height = width) => {
  ensureDir(dirname(output));
  execFileSync("rsvg-convert", [
    "--output",
    output,
    "--width",
    String(width),
    "--height",
    String(height),
    input,
  ]);
};

for (const tone of ["graphite", "paper"]) {
  const source = resolve(currentDir, `lazu-icon-${tone}.svg`);
  const outputDir = resolve(rasterDir, tone);
  for (const size of ICON_SIZES) {
    const output = resolve(outputDir, `lazu-${tone}-${size}.png`);
    renderSvg(source, output, size);
    copy(output, resolve(packagePngDir, `lazu-${tone}-${size}.png`));
  }
}

const graphiteRaster = (size) =>
  resolve(rasterDir, "graphite", `lazu-graphite-${size}.png`);

copy(graphiteRaster(16), resolve(webPublicDir, "favicon-16x16.png"));
copy(graphiteRaster(32), resolve(webPublicDir, "favicon-32x32.png"));
copy(graphiteRaster(48), resolve(webPublicDir, "favicon-48x48.png"));
copy(graphiteRaster(150), resolve(webPublicDir, "mstile-150x150.png"));
copy(graphiteRaster(192), resolve(webPublicDir, "android-chrome-192x192.png"));
copy(graphiteRaster(512), resolve(webPublicDir, "android-chrome-512x512.png"));

const appleTouchSource = resolve(currentDir, "lazu-apple-touch-icon.svg");
const appleTouchRaster = resolve(webPublicDir, "apple-touch-icon.png");
renderSvg(appleTouchSource, appleTouchRaster, 180);
copy(appleTouchRaster, resolve(packagePngDir, "apple-touch-icon.png"));

execFileSync("magick", [
  graphiteRaster(16),
  graphiteRaster(32),
  graphiteRaster(48),
  resolve(webPublicDir, "favicon.ico"),
]);
copy(
  resolve(webPublicDir, "favicon.ico"),
  resolve(packagePngDir, "favicon.ico"),
);

// The primary raster keeps the lockup's own aspect ratio: the width is fixed
// at 1280 and the height follows the viewBox, so the seal stays a circle.
const PRIMARY_RASTER_WIDTH = 1280;
const PRIMARY_RASTER_HEIGHT = Math.round(
  (PRIMARY_RASTER_WIDTH * 80) / PRIMARY_VIEWBOX_WIDTH,
);
const primaryGraphite = resolve(currentDir, "lazu-primary-graphite.svg");
const primaryRaster = resolve(rasterDir, "lazu-primary-graphite-1280w.png");
renderSvg(
  primaryGraphite,
  primaryRaster,
  PRIMARY_RASTER_WIDTH,
  PRIMARY_RASTER_HEIGHT,
);
copy(primaryRaster, resolve(webPublicDir, "logo.png"));
copy(primaryRaster, resolve(webFullDir, "logo-dark.png"));
copy(primaryRaster, resolve(packagePngDir, "lazu-primary-graphite-1280w.png"));

// Payment e-mails place the Primary lockup on an ink masthead. Export the
// canonical Paper variant as a PNG because common mail clients omit SVGs.
const primaryPaper = resolve(currentDir, "lazu-primary-paper.svg");
const primaryPaperRaster = resolve(rasterDir, "lazu-primary-paper-1280w.png");
renderSvg(
  primaryPaper,
  primaryPaperRaster,
  PRIMARY_RASTER_WIDTH,
  PRIMARY_RASTER_HEIGHT,
);
copy(primaryPaperRaster, resolve(webPublicDir, "lazu-email-logo-paper.png"));
copy(primaryPaperRaster, resolve(packagePngDir, "lazu-primary-paper-1280w.png"));

// The share image is designed in social/lazu-og-1200x630.html and rendered to
// PNG by hand (see that file); this script only distributes the committed PNG.
const ogRaster = resolve(socialDir, "lazu-og-1200x630.png");
copy(ogRaster, resolve(webPublicDir, "og-image.png"));
copy(ogRaster, resolve(packagePngDir, "lazu-og-1200x630.png"));

console.log(
  `Generated Seal logo system: ${ICON_SIZES.length} sizes × 2 themes, Web, Docs, download package, favicon, PWA and OG assets.`,
);
