# Yunmu Console system

**Status: LOCKED**  
**Locked: 2026-08-20**  
**Source exploration:** `archive:brand/explorations/console-f3-2026-08/ia-4skins.html`, skin `V2 云母`  
**Locked IA refinement:** `S1 钱包独立`（历史选择板已在决议固化后清理）
**Locked settings refinement:** `settings-ui-2026-08` option 1 + admin option B, spacing `松`
**Canonical registry:** `../BRAND.md`, section `02 云母 Yunmu (Console)`

> **G1 (2026-10-06):** the ground, the floating sidebar, translucent surfaces,
> the periwinkle evidence colour and the ink chart series below are superseded —
> see `BRAND.md` § "Locked visual direction · G1". Layout, IA, settings and
> interaction rules in this file still hold.

## Scope

Yunmu is the locked visual and interaction foundation for the whole product.
It supersedes rule 2 of 01 Kumo Native: the warm orange action colour is retired
everywhere — console, marketing and docs alike — and is not reintroduced as a
route-scoped exception. The tokens therefore live on `:root` in
`brand/tokens/colors.css`, so portalled dialogs and prerendered marketing pages
resolve the same palette without any runtime route class.

Console-specific rules below govern `/console/*` layout and interaction; they
describe the console's composition, not a second colour system.

## Locked recipe

1. Three-layer warm-paper ground, two ambient lights and a masked 64px grid.
2. Floating translucent 24px-radius sidebar with one navigation column.
3. Ink/paper primary actions; periwinkle is evidence and focus, not a large fill.
4. Charts use the ink series (`--lazu-chart-1/2/3`, locked 2026-09-30, see "Locked console overview"). Periwinkle stays the evidence color for links and highlights, not a chart fill. Orange is not a console chart fill.
5. Selection uses only neutral background, weight and foreground.
6. Shared UI components remain canonical; pages compose them instead of
   creating private button, modal, table or field systems.
7. Long editing and detail tasks use centered modals. One-to-three-field table
   edits may expand inline. Drawers are retired from the console direction.
   Refined 2026-08-28: a read-only detail that belongs to exactly one row —
   the request receipt in Usage & Logs is the case this was written for —
   expands inline beneath that row instead. Reading the detail is a
   continuation of scanning the table, not a separate task, so it must not
   move the reader off their position; the previous implementation rendered
   the panel after the table and pagination and called `scrollIntoView`,
   which cost the reader their place in the list. This carve-out is for
   reading only. Editing of any length still uses a centered modal.
   Refined 2026-09-02: the platform customer-account ledger is the second
   explicit exception. Selecting a customer opens one right-side operational
   drawer so operators can scan adjacent accounts with J / K without losing
   the ledger. The drawer owns that account's existing user, key, usage,
   wallet, client and audit contracts; irreversible or permission-changing
   actions still require an explicit confirmation modal. This exception does
   not reintroduce drawers as a general page pattern.
8. Motion remains direct and restrained, and respects reduced-motion settings.
9. Silent scroll (locked 2026-08-22). Scroll containers paint no scrollbar —
   no thumb, no track, no reserved gutter. A grey rail running down a floating
   paper panel breaks the sheet illusion the system rests on. Scrolling stays
   fully functional: wheel, trackpad, touch, keyboard (arrows, PageUp/PageDown,
   Home/End) and `scrollIntoView` all behave normally, and focus outlines still
   move with the content. The single implementation is the `*` rule at the end
   of `brand/tokens/ui.css`; components must not redeclare `scrollbar-width` or
   `::-webkit-scrollbar`, and must never paint a thumb or track back in.
   Because the affordance is gone, a scrollable region has to signal its own
   depth some other way — a cut-off row at the fold, a count in the footer, or
   a sticky header — rather than relying on the bar to say "there is more".

## Locked console information architecture (2026-08-21)

The selected production direction is Console Refit R2 option S1, “钱包独立”.
It is a refinement of Yunmu, not a separate component or visual system.

1. The five primary user tasks are Overview, API Keys, Usage & Logs, Wallet,
   and Personal Settings. The Models catalog remains a workspace navigation
   entry, and external Docs remains reachable regardless of module flags.
2. Wallet owns balance, period spend, daily reconciliation from the
   authoritative wallet ledger, Stripe top-up, and top-up history.
3. Usage & Logs owns Request Logs, Monthly Report, and Files as tabs.
4. Personal Settings owns profile, sessions, preferences, and the effective
   access summary previously shown on the standalone Access page.
5. Retired Billing, Top-up, Files, Access, Monthly Report, and Log URLs no
   longer resolve. Platform-admin navigation remains role-adaptive and is not
   collapsed into the five user tasks.

## Approved production references

- Signature layer: `apps/web/src/styles/yunmu.css`
- Console shell: `apps/web/src/components/layout/PageLayout.tsx`
- Canonical tokens: `brand/tokens/colors.css`, `brand/tokens/ui.css`,
  `brand/tokens/data-viz.css`
- Model plaza: `archive:brand/explorations/console-f3-2026-08/pg-models-final.html`
- Pricing operations: `archive:brand/explorations/console-f3-2026-08/pg-pricing-final.html`
- Customer account ledger:
  `archive:brand/explorations/platform-accounts-2026-09/a-v3.html`
- Earlier organization and member concepts (superseded for the account page):
  `archive:brand/explorations/console-f3-2026-08/pg-orgs-final.html` and
  `pg-members-final.html`

Organization and member management must not claim working impersonation,
membership or budget controls until registered backend contracts exist. The
approved mock defines the intended interaction, not permission to fabricate
production data.

## Locked customer account ledger (2026-09-02)

**Selected exploration:** `archive:brand/explorations/platform-accounts-2026-09/a-v3.html`

The selected surface is a customer ledger plus one account drawer, not a new
organization profile route. Platform members are excluded from the customer
cohorts and remain under Settings › Accounts & access. All six facts are real
server-side filters; search deep links resolve an exact username or ID. The
drawer preserves the ledger position, supports adjacent-account navigation and
links back to the existing Key Inspector, Wallet and Usage & Logs surfaces.
Price factor and cumulative top-up are shown as evidence from existing
contracts. Tier, balance, access and role changes use the registered endpoints;
destructive or permission-changing actions require confirmation.

## Locked settings surfaces (2026-08-27)

The selected production direction is option 1 from
`archive:brand/explorations/settings-ui-2026-08/v3.html` for both personal and platform
settings, with the `松` key/value spacing preset. Platform settings use option
B from `admin.html`. These files remain decision evidence; the rules below are
the canonical production constraint.

1. A settings row reads `label | value + actions | explanation`. The action is
   part of the value cluster and stays adjacent to the value; it is never
   pushed to the far edge of the page. Platform settings ship a
   `minmax(240px, 280px)` label column and a 36px gutter at >=1180px — set on
   `.settings-page` in `console-review.css`, over the 240–300px / 40px
   defaults `settings-surface.css` keeps for personal settings — which is
   what the value column needs before a switch plus a template preview starts
   wrapping; below that the explanation stacks under the label in two columns,
   and below 640px everything reads as one column (rule 5).

   Platform settings does not opt into the 868px form cap. It was capped for a
   while, and the three-column row does not fit in 868px — the explanation
   landed about 600px from the label it explained, which is what the 2026-09
   audit filed against the page. The fix that landed then forced two columns at
   every width instead, which pushed every control into a lone column against
   the right margin. Releasing the width is what makes the locked order work.

   Personal settings keeps the stacked pair: it is narrower and its
   explanations are longer.

   2026-10-01 refinement (personal settings only): one 760px column, head
   included, rows `label + explanation | value + action` (`1fr | 288px`) at
   every width above 640px. Three cards — 账号 (username, email, password,
   linked sign-ins), 邮件 (balance alert, weekly report + preview, product
   updates, send time zone, one footnote for the mails that always go out) and
   数据 (request-IP logging, delete account as an outlined row). A missing
   email is said once, as a notice above the cards. Every switch saves when it
   flips. Retired from the page because they repeated the top bar or held no
   choice: language, theme, current session and its logout, the access token,
   role and permission rows, the always-sent rows and the per-user
   unpriced-model opt-in (now an operator decision; the gateway refuses
   unpriced models for everyone).

2026-09-13: the settings groups render as one card each, with the group's
deployment status as a band under its header and a floating unsaved-changes
pill. Retired from the surface: `Notice` (nothing requested `/api/notice`;
announcements own that job). `DataExportDefaultTime` stays — the dashboard reads
it for its first bucket size, offered as hour, day or week only.
`LogConsumeEnabled` stays: nothing loads it from env or
yaml, so the console switch is the only way back on. Merged:
`QuotaRemindThreshold` folds into `email_setting.low_balance_threshold_usd`,
which the gateway already read whenever low-balance mail was enabled — a
startup migration carries the old value over. Renamed: `DataExportEnabled` is
"定时汇总", because it drives the usage rollup the dashboard reads and exports
nothing. `PasswordLoginEnabled` stays off the surface: its option row is
deleted at startup with the retired GitHub OAuth keys, so a switch would
silently revert.
2. Explanations use the muted text token on a solid surface. Inline changes
   expand inside the row they affect. Destructive or invalidating actions use
   restrained warning/danger treatments and explicit confirmation rather than
   a permanent red slab.
3. Personal settings group profile, authentication, sessions, preferences and
   dangerous account actions on the same row system. A login method may only
   be presented as available when its account prerequisite and deployment
   capability are both true.
4. Platform settings are grouped by administered object into four tabs:
   Accounts & access, Billing, Models & upstreams, and Observability.
   Legacy query values redirect to the canonical tab so bookmarks remain
   valid. A setting has one owner; duplicate forms for the same option are not
   allowed.
5. Below 640px the fixed desktop columns collapse to one reading column in the
   order label, value/actions, explanation. Fields and confirmation controls
   wrap within the card; the desktop measurements must never cause horizontal
   clipping on a 320px viewport.
6. Billing owns money policy and nothing else, in the order a payment moves
   through the system: how money arrives (signup grants), how a
   request is charged (pre-consumed quota, USD-only amounts), when the gateway
   refuses (low-balance reminder threshold), and how the user is told (receipt
   and pending-payment mail). Per-model prices are Model Operations, not
   Billing — a price is an attribute of the model it belongs to. Anything with
   its own lifecycle (a record that is created, prioritised and expires, such
   as a pricing adjustment) is an operations object with its own workbench,
   never a settings form. Credentials supplied by env or yaml are deployment
   state, not settings: when a page reports them it is as one status line,
   never as read-only form rows. A status line is added only where the reader
   can act on it — Billing carries none (2026-09-13, owner decision): "Stripe
   connected" changed nothing the page lets anyone do, while the mail group
   keeps its line because it carries the test-send action and the probe group
   keeps its line because an env override silently owns the controls below.
7. Account tiers are rate policy, not billing. They live under Accounts &
   access as "速率与等级". The word 档位 belongs to Model Operations' outward
   price lanes (直连 / 便宜) and must not be reused for account levels.

## Locked list rows (2026-08-28)

One record in a list is one measurement, and it is the same measurement in
every console list. Before this rule six specs coexisted — the shared table at
`9px 12px`, model operations at `10px 15px`, announcements at `11px 14px`,
wallet at three of its own (`10px 16px`, `14px 18px`, `16px 20px`),
organizations at `13px 17px` — and not one of them landed on the spacing
scale, so the same kind of row looked slightly different on every page.

1. A list row is `var(--lazu-row-padding-y) var(--lazu-row-padding-x)`, which
   is `space-3 space-4` (12px / 16px). Values are `--lazu-row-text` (13px);
   column headers and secondary text are `--lazu-row-label` (12px). This
   applies to organizations, wallet, usage & logs, API keys, model operations
   and announcements alike.
2. Pages do not restate these numbers. They come from the shared table styles
   in `apps/web/src/styles/ui.css`; a page that needs a list renders the
   shared table rather than hand-rolling padding.
3. Settings rows are not list rows. They keep the label column and gutter
   locked in the settings surface rules above (rule 1), and are unaffected.

## Locked channel health surface (2026-09-01)

**Selected exploration:** `archive:brand/explorations/channel-stability-2026-09/r2-v3-badge-heatwall.html`
(the C face), with the A face taken from `r2-v1-inline-matrix.html`.

Every model runs on two lanes, stable and eco, backed by different upstream
pools. A model-level health figure hides the only comparison that matters, so
stability is presented per lane everywhere it appears. Four placement options
were explored and the pair below was selected; the remaining explorations are
decision history.

1. **`/console/health` is the heat wall.** Rows are `model × lane`, twenty-four
   equal slices across the selected window, with a fixed detail panel for the
   selected row. A slice is an hour on the 24h view and proportionally longer on
   the wider ones, so the response publishes `bucket_seconds` and the UI must
   read the slice duration from it rather than asserting "hourly".
   The alternative — a paginated `model × channel` table — was rejected because
   at roughly two hundred routes the operator cannot see the platform at once,
   which is the entire reason the page exists.
2. **The model plaza carries the same numbers inline.** Lane health renders in
   the existing lane row as a tone dot plus availability and p95. Choosing a
   model and judging its lanes is one decision, so it happens in one place.
3. **A probe is a sample of the lane; where it came from is an operator
   detail.** (Revised 2026-09-24, and again 2026-09-26.) A probe is a small
   request of the lane's own kind, counted with customer traffic for every
   figure. A cell is one mark per verdict in one weight, for everyone: the
   hollow probe cell and the faded carried cell are retired, because a wall of
   uneven greens described our probe schedule rather than the upstream. A
   cell holds only what was measured inside its own slice — a verdict is never
   carried forward into an hour nobody looked at — and the probe cadence
   (45 minutes; 2h40m for image generation) aims to sample each slice.
   Budget limits, outages and skipped sweeps may still leave a slice neutral.
   The sample's source and count live in the tooltip, and only an operator's
   tooltip names them. A route that cannot be checked stays hatched, for
   operators only; no data stays neutral. A lane's status colour is the
   router's breaker verdict on the upstreams it would choose now, never a
   threshold on the window's percentage.
3a. **One section per kind of model, each with its own figures.** (Locked
   2026-09-26, canvas `archive:brand/canvases.md`.)
   Kind is what the model produces, read from the catalog's output modality:
   大模型 · 向量模型 · 图片生成 · 搜索工具, with 语音 and 视频生成 appearing
   when such models ship. Every section is 可用率 + one speed + one tail:
   大模型 = 可用率 · 首字 p50 · 首字 p95 · 输出 TPS · 中断率; 向量模型 =
   可用率 · 延迟 p50 · p95; 图片生成 = 可用率 · 生成耗时 p50 · p95, drawn in
   three-hour slices; 搜索工具 = 可用率 · 响应 p50 · p95, one row per provider
   and no lane. Cache hit and truncation are not lane properties — they
   describe what callers sent — and are not shown here. "Content came back
   broken" (an undecodable image, an empty fixed-query search probe, an
   all-zero vector) counts against availability rather than getting a column
   of its own.
4. **Lanes are the user-facing unit; channels are not.** Members see lane
   figures. The upstream pool behind a lane, and which vendor served what
   share, is admin-only. Switching a lane's upstream must not be a
   user-visible event.
5. **Data marks use `--lazu-radius-datamark`.** Heat cells, lane dots and
   legend swatches are plotted marks rather than controls and share one
   canonical radius; pages do not introduce their own.

## Locked model status cards · C′ (2026-09-27)

The owner chose C′ in Claude session `83ad64b0-5391-4173-989c-3d8910ecdc3e`
at 13:31 UTC and approved the seven text simplifications at 13:55 UTC.
Canvas: `archive:brand/canvases.md`.
This amendment supersedes the earlier lane-row layout and selected-row panel
above; it preserves the lane as the unit of evidence and comparison.

- One card per model, with each actual lane's status, seven-day availability,
  history and figures together in its own block. Three columns on desktop,
  two below 1100px, one on phones; cards keep their intrinsic height. Model
  families stay expanded, in fixed order, including single-model families.
- A genuinely single-lane model omits the redundant lane label. Discount-only
  models say `仅 Agent 专线` once in the card head. Filtering must not turn a model
  with both lanes into a discount-only model. Operator-created routes retain
  their real slug in the operator view instead of several identical labels.
- The global incident banner is admin-only and compact. Healthy families
  omit their summary. The healthy lane may use its shared healthy shape with
  screen-reader-only status text; other statuses retain visible words and the
  shared distinct shapes. This is a scoped exception to the visible healthy
  word requirement in component-contracts.md, not a color-only status system.
- LLM figures retain first-token p50/p95 and output tok/s. Broken-stream rate
  is called `截断`, and appears only at >= 2%, with warning emphasis. This is
  the owner's explicit exception to the earlier always-visible rate. It is
  still interruption_rate (a stream failing after output starts), not normal
  max-token truncation. Embedding/rerank/audio use latency p50/p95,
  image/video generation p50/p95, and search response p50/p95.
- The window is seven days. Slice durations come from the response: normally
  two hours, six for images, superseding the old three-hour image copy.
  `暂无数据` is the common no-evidence wording; it does not mean healthy.
- No required metric or model identifier may be clipped by the compact card;
  wrap them as necessary. Card typography consumes canonical health tokens,
  and all colors, radii and status markers remain shared Brand UI.
- Each timeline contributes one Tab stop. Left/Right and Home/End inspect
  slices, Tab advances to the next lane, and Escape closes the detail.
  Pointer/focus state stays local to a memoized lane.

## Locked Seal refinement (2026-09-21)

Owner-approved continuation of the 2026-09-20 Seal canvas
`archive:brand/canvases.md`, reconfirmed during PR #252 repair.

- Console page headings use Instrument Serif at `--lazu-console-heading-size`.
  Display money uses `--lazu-console-money-size` or `--lazu-display-stat`,
  regular weight and tabular figures. Body, controls and dense table values
  retain Geist / IBM Plex Mono; serif is never used below 20px.
- The Models catalogue is explicitly excluded: preserve its card structure,
  sans price typography and semantic status colours. Do not replace it with
  the canvas price table or apply the console money override to its cards.
  (Amended 2026-10-14: its console page heading now follows the shared
  console heading like every other page; the cards stay excluded.)
- Desktop chrome shows visible labels beside update, theme, language and help
  icons. The account keeps its avatar and accessible name. Small-screen chrome
  keeps its existing labelled navigation and controls.
- Platform administration lives in a bottom sidebar menu. Its items retain
  existing role/module gates and direct links; this is navigation disclosure,
  not a permission or tenant-scope switch. Workspace links remain visible.
- Authenticated authorization failures retain application navigation. Unknown
  public URLs keep the public shell while only their content waits.

This amendment supersedes the public-only display restriction for these
console headings and display-money values, and Quiet Precision's bold KPI
rule for display money only. All other data typography remains unchanged.

## Locked console overview (2026-09-30)

Owner-approved canvas `archive:brand/canvases.md`
(pages 3 and 5). Implemented on `design/overview-redesign-2026-09`.

- **Structure A3** on both planes. Platform: metrics grouped 服务质量 (success
  rate is the largest figure; red below 99%) / 收入 (收入, 毛利) / 用户增长
  (新注册, 活跃, 流失); charts 成功率 (99% target line), 收入·成本·毛利,
  用户增长; a 新用户漏斗 panel (注册 → 首个请求 → 首次充值); lists 模型成功率
  (worst first), 用户毛利, 充值到账; a 客户端 card. Workspace: 消耗 / 可用余额 /
  预计还能用 / 成功率 / Tokens · 请求 (one cell, tokens first); 每日用量 chart
  (tokens bars, spend line); 最近的会话.
- **Charts: 墨 + 柱与线.** Volume as thin bars, the rate or money as a smooth
  line above; `--lazu-chart-1` ink for the measure, `--lazu-chart-2` for its
  companion, `--lazu-chart-3` for volume; only failure uses danger red.
- **Ranges**: 24 小时 (hourly, default) / 7 天 / 30 天 (daily) / 90 天 (weekly)
  / 自定义. Comparisons are against the previous equal range ("较昨天同时段"
  for 24 小时).
- **Counts** always compact to k / M / B (`components/ui/metric-format.ts`).
- **Sessions** live under 用量与日志 as a 会话 tab (list A) with a full-page
  detail (first message, swimlane timeline, threads, requests). Agent terms
  stay English as mono tags: `main`, `subagent`, `compact`, `review`.


## Locked page-UX revision (2026-10-14)

Owner-approved canvas `archive:brand/canvases.md`
(audit: `archive:brand/explorations/page-audit-2026-10/index.html`). Five shared
rules, then the pages built on them.

1. **One page head.** Every console page's title sits at the same place —
   12px / 16px insets inside the content column (290px from the window edge
   at 1440), 10px under the top bar (86px), Instrument Serif at
   `--lazu-console-heading-size`. Actions align to the title's bottom and
   never push it down. The overview's own head and the Models page use the
   same geometry.
2. **Two switch controls.** Peer views (a different block of content) use the
   underline strip in the page head — ink label, 2px `--lazu-evidence`
   underline. Filters on one table use the segmented box — a 4% ink track,
   the picked segment a white sheet with a hairline, 10px / 7px radii. Solid
   ink pills and bare text pills are retired.
3. **Notices on the paper.** In-page notices — including the health page's
   banner — are white with one `--lazu-notice-edge`; the state is carried by
   the dot / icon and the lead's colour only.
4. **Empty answers.** An actionable empty state is a dashed slot with its one
   action in the place the content will take; a non-actionable one is a single
   plain sentence. No header row over nothing, no skeleton that never resolves,
   no row of empty health cells. Tables reserve a page of rows only while the
   page is in flight.
5. **Theme switch.** Two monochrome icon segments in the segmented box; 150ms
   direct ease, no amber, no glow, no spring.

Pages: the overview's chart reads the latest bucket out beside its legend and
shows the tip only on hover; Models and model status lay out one grid per kind
with the maker on each card; the wallet without a payment method shows a
contact slot in the top-up's place; usage keeps one filter row (time · lane ·
search · 筛选) with removable chips and its four views on the head strip;
platform settings rows share one control column so every explanation starts at
the same x; the phone shell shows only the page name, service status and
updates in its top bar, and its tab bar uses the sidebar's names.
