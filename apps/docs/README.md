# Lazu Docs

`apps/docs` is the active Vocs documentation site, served at `https://lazu.ai/docs` (Vocs `basePath: "/docs"`; the old `docs.lazu.ai` host 301s there).

- Root locale is English.
- Simplified Chinese, Traditional Chinese and Japanese pages live under
  `/zh/`, `/zh-TW/` and `/ja/`. Keep the same topic paths in all four locales.
- OpenAPI assets are synced from `packages/contracts/openapi/lazu-api-reference.v1.yaml` during `predev` and `prebuild`.
- The nginx container serves static files on port `80`; old `/docs/*` URLs redirect to root paths for compatibility.

Local commands:

```bash
npm run typecheck
npm run lint
npm run build
```

## Authoring sources and generated output

| Change | Edit | Generated output |
| --- | --- | --- |
| Tutorials and examples | `pages/**/*.mdx` | Per-route `public/**/*.md`, LLM text and rendered pages |
| API reference | `../../packages/contracts/openapi/lazu-api-reference.v1.yaml` | `public/openapi*` and compatibility copies under `public/docs/` |
| API reference translations | `i18n/openapi/{en,zh,zh-TW,ja}.json` | Localized OpenAPI JSON |
| Navigation | `vocs.config.ts` | Site sidebar and locale navigation |
| Design tokens | `../../brand/tokens/` | `public/brand.generated.css` via `brand:sync` |

`npm run sync:openapi` generates API assets, route Markdown, LLM text, sitemap
and robots files. `predev` and `prebuild` run it automatically. Edit the sources
above instead of generated public files; rebuilding will overwrite them.
The generated route Markdown and API/LLM assets are ignored by Git.

The separate web app runs `apps/web/scripts/generate-seo-assets.mjs` during its
build. That script owns the web app's public Markdown, including localized
home/FAQ text and copies of the Go legal defaults. Do not hand-edit those copies.

From the repository root, `make test-docs` checks maintainer links, old path
conventions and locale topic parity. It does not certify API behavior,
translation accuracy, external URLs or live model availability.
