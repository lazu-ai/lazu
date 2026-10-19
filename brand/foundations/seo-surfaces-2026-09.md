# SEO surfaces and 运营 — locked 2026-09-29

**Status: LOCKED**
**Approved canvas:** `lazu-ops:brand/canvases.md` (source `lazu-ops:brand/explorations/seo-surfaces-2026-09/`)
**Research:** `lazu-ops:docs/audits/2026-09-29-seo-geo-audit.md`

## Public surfaces

| Surface | Locked direction | Implementation |
| --- | --- | --- |
| Public header | 模型 · 文档 · 博客 · 常见问题; Key 查询 and the external Changelog link leave the nav; current section = neutral fill. Worn by every public page, app-owned ones too (payment returns, invites, 403). Languages are `<a href>` links to this page in each locale (crawlable; the static copy repoints them at the page's hreflang alternates). Signed-out actions 登录 / 免费开始 are what the prerender and crawlers see; a signed-in visitor gets 控制台 after load | `components/marketing/PublicHeader.tsx`, `public-site/chrome.ts` |
| Public footer | Four columns (产品 / 开发者 / 公司 / 法律) + legal line `© DUBRIO LLC · Delaware, USA · Lazu is operated by DUBRIO LLC`. No language or theme control (header only, 2026-09-30) | `components/marketing/PublicFooter.tsx`, docs `public/site-footer.js` |
| /about | Operator facts ledger (DUBRIO LLC, Delaware, contact, invoices, governing law) + machine-readable addresses | `pages/About` |
| /models | Maker-grouped cards, rendered by the API so prices are in the HTML; each card links to its model page | `internal/publicsite/templates/models.gohtml` |
| Model page | **A · single column**: lede sentence with prices, facts, price table with official row and 7-day bars, capabilities, sample, questions, same family, CTA. B (workbench split) is archived | `templates/model.gohtml` |
| Blog | Ledger list with small covers; 720 article with cover, conclusion first, `::model` / `::prices` blocks read live from the catalog | `templates/blog.gohtml`, `post.gohtml` |
| Covers | Five layouts, picked per post in the editor and exported as the 1200×630 share PNG: 改一行 (base_url diff, default for tool guides), 价目单 (stable-lane prices read from the catalog, default for launches), 模型 ID (ids sized to fit whole, default for comparisons), 刊头 and 大印章 (any post). A layout missing its data (no models / no prices) falls back to 刊头. Prices are never rounded. Until a PNG exists the public page draws the paper / ink card | `pages/Console/Ops/BlogCover.tsx`, `templates/cover.gohtml` (fallback); canvas `lazu-ops:brand/canvases.md` |
| Docs | Served at `lazu.ai/docs`; top bar gains Models; `docs.lazu.ai/*` answers 301 | `apps/docs` |

The operator's legal name appears only in the footer line, /about, the legal
documents and the Organization schema (`legalName`) — never in titles or H1.

## 运营 (console)

平台管理 「公告」 is renamed 「运营」 with four tabs:

- **上新** — one record per model launch, opened automatically when a model in
  the public catalog is new; rows for model page, poster, notice, blog, share
  copy and search-engine ping, each read from the table that owns it.
- **公告** — the existing notice workbench.
- **博客** — one table of posts from three sources (editor, model draft,
  Markdown import); published only from the editor.
- **配图** — the poster studio plus the 1200×630 blog cover and X 16:9 formats.

Blog editor: 50/50 Markdown | live preview rendered by the public templates,
right pane tabs 预览 · 设置 · 搜索结果, publish confirmation that blocks on
`[placeholders]`, broken links or missing models.
