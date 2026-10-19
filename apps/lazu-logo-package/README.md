# Lazu · Seal Logo Package

Version 3.0 · 2026-09-20

This download package mirrors the locked Lazu Seal system in
`brand/assets/logo/`. The generator updates the canonical assets, Web, Docs,
and this package together; files in this directory are not a separate source
of truth.

## Contents

```text
lazu-logo-package/
├── svg/
│   ├── lazu-icon-{graphite,paper}.svg
│   ├── lazu-primary-{graphite,paper}.svg
│   ├── lazu-stacked-{graphite,paper}.svg
│   ├── lazu-wordmark-{graphite,paper}.svg
│   ├── lazu-apple-touch-icon.svg
│   └── favicon.svg
└── png/
    ├── lazu-{graphite,paper}-{16..1024}.png
    ├── apple-touch-icon.png
    ├── lazu-primary-graphite-1280w.png
    ├── lazu-og-1200x630.png
    └── favicon.ico
```

## Use rules

- Use **Graphite** on light/default backgrounds and **Paper** on dark
  backgrounds. Choose from the logo's actual local background.
- Use **Icon** for compact navigation and app icons, **Primary** for horizontal
  brand positions, **Stacked** for centered layouts, and **Wordmark** only when
  the icon already appears in the same visual area.
- Minimum digital size is 16px for Icon and 96px visible width for Primary.
- Use the dedicated opaque `apple-touch-icon.png` for iOS home-screen links;
  do not substitute a transparent browser or PWA raster.
- Do not stretch, rotate, recolor, outline, add gradients or shadows, or alter
  the Seal geometry.

Full rules: `brand/foundations/logo-system.md`.

## Regenerating

From the repository root:

```bash
node brand/assets/logo/generate-assets.mjs
```

The command requires `rsvg-convert` and ImageMagick 7's `magick` executable.
Do not edit generated SVG, PNG, or ICO files by hand.
