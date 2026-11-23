# Yunmu Console system

**Status: LOCKED**  
**Locked: 2026-08-20**  
**Locked IA refinement:** `S1 钱包独立`
**Locked settings refinement:** option 1 + admin option B, spacing `松`
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
   Refined 2026-11-19: no side drawers anywhere, with no exceptions. The
   2026-09-02 customer-account drawer exception is withdrawn: account detail
   opens as a centered dialog over the ledger and keeps J / K to move between
   adjacent accounts. Any detail or editing surface, platform pages included,
   opens as a centered dialog; on phones the dialog becomes a full-screen
   sheet, never a panel sliding in from an edge. Irreversible or
   permission-changing actions still require an explicit confirmation.
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

## Locked dialog and control base (2026-11-20)

Base A, "dialogs hold the line": editing stays in centred dialogs, and the
dialog itself is what was fixed. Rules, each enforced in `components/ui`:

1. Buttons have no default look. `Button` requires `variant`; leaving it out
   is a type error. In a dialog footer the right-most button is the ink
   primary and the way out sits to its left. A destructive confirmation is
   solid red (`danger`); the entry that opens it is red text on white.
2. Menus and popovers are one sheet of paper: control radius (12px), a
   hairline, 4px inset, 8px rows, the picked row tinted evidence teal with
   the check at the right. A popup portalled to body carries the page's
   control recipe class itself instead of inheriting it. Never a pill.
3. One switch: a 36×20 track, evidence teal when on, 18% ink when off, a
   16px white knob. Radii come back to the tokens: control 12, card 18,
   overlay 20.
4. A dialog is three bands. Header and footer are pinned and only the body
   scrolls; the dialog is at most 86vh tall. Every `Modal` gets this; no
   page has to opt in.
5. Picking something from inside a dialog swaps the dialog's page and comes
   back with the form as it was. A second dialog is never stacked over the
   first, except an explicit confirmation of an irreversible step.
6. Empty: when the reader can act, a dashed slot with one button; when they
   cannot, one sentence. An empty table shows no header row.
7. Load failure inside a surface: a white card with one ink line, a danger
   icon and a danger title, the retry at the right. The state shows only in
   the icon and the title colour.
8. Phones: one breakpoint, 768px. Below it every dialog is a sheet from the
   bottom edge with a 44px close in the top-right corner and no drag handle;
   two footer actions share the bar a third and two thirds, 44px tall.

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

Organization and member management must not claim working impersonation,
membership or budget controls until registered backend contracts exist.

## Locked customer account ledger (2026-09-02)

The selected surface is a customer ledger plus one account detail dialog
(centered dialog since 2026-11-19, per rule 7; replaces the earlier drawer),
not a new organization profile route. Platform members are excluded from the
customer cohorts and remain under Settings › Accounts & access. All six facts
are real server-side filters; search deep links resolve an exact username or ID.
The detail dialog preserves the ledger position, supports adjacent-account
navigation and links back to the existing Key Inspector, Wallet and Usage & Logs
surfaces. Price factor and cumulative top-up are shown as evidence from existing
contracts. Tier, balance, access and role changes use the registered endpoints;
destructive or permission-changing actions require confirmation.

**Account detail dialog (2026-11-20).** The drawer is replaced by one
centred dialog, 1160 px wide, laid out in two columns:

1. The left column is 380 px on a faintly tinted ground. It holds the avatar
   with previous, next and copy-link, then the name, email and last activity,
   and badges for role, status and low balance. One row follows per setting
   (余额, 专属折扣, 速率, 接入地址, 显示名称, 备注): label, value, a caption,
   and one text action. That action opens the row's fields on a quiet panel
   in place, with 取消 and 保存. The account actions sit at the column's foot.
2. The right column is the last 30 days: three figures, a bar per UTC day
   whose tooltip gives requests and errors, and spend by model. Tabs follow
   for API Keys, the ledger and sessions. An order still waiting for payment
   is a tinted row at the top of the ledger with 补单, which confirms the
   amount, method and order number before crediting.
3. A rate row carries a gauge of the 30-day peak against the effective
   limit. The gauge is warning-coloured from 60 % and danger-coloured from
   90 %.
4. On a phone the dialog is a full-screen sheet and the columns stack.

## Locked settings surfaces (2026-08-27)

The selected production direction is option 1 for both personal and platform
settings, with the `松` key/value spacing preset. Platform settings use option
B. The rules below are
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
Consumption logging is always on. The retired hourly usage export has no
switch. The dashboard reads its own rollups.
`QuotaRemindThreshold` folds into `email_setting.low_balance_threshold_usd`,
which the gateway reads for low-balance mail. A startup migration carries the
old value over. `PasswordLoginEnabled` stays off the surface: its option row is
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

Selected direction: badge heatwall (C face) with inline matrix (A face).

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
3a. **One section per kind of model, each with its own figures.** (Locked 2026-09-26.)
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

The owner chose C′ and approved the seven text simplifications on 2026-09-27.
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

Owner-approved continuation of the 2026-09-20 Seal direction, reconfirmed during
PR #252 repair.

- Console page headings and display money were set in Instrument Serif
  (superseded 2026-11-21, see [Console type](#console-type-2026-11-21)).
  Body, controls and dense table values retain Geist / IBM Plex Mono; serif
  is never used below 20px.
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

## Console type (2026-11-21)

Owner-approved before/after canvas (2026-11-21). The console is set in one
sans face; the serif stays on the wordmark and the public pages (home, sign-in,
key lookup, docs).

- Page titles: Geist 600 at `--lazu-console-heading-size` (26px),
  letter-spacing −0.02em, the same on every page and on phones.
- Display money (overview cards, usage spend, payment record, wallet balance):
  Geist 600 with tabular figures and letter-spacing −0.025em (−0.035em at
  44px and above), so money sits in the same face as the rates and counts
  beside it. Usage spend is 44px (40px on a phone). These sizes are maximums:
  an amount too long for its column is set smaller, never wrapped, cut or
  run into the next column, and the row keeps its height.
- Mono stays for identifiers, code and chart axes. Page titles and display
  money never use `--lazu-font-display`; the status-page mark
  (`paper-states.md`) and artwork keep it.

This supersedes the heading and display-money lines of the 2026-09-21 Seal
refinement and the "Instrument Serif" wording of the 2026-10-14 page head.

## Locked console overview (2026-09-30)

Owner-approved overview A3 and sessions layout, implemented on `design/overview-redesign-2026-09`.

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


## Locked model detail (2026-11-19)

A model opens from 模型运营 as one centered dialog, up to 1080px wide (a
full-screen sheet on a phone), with no tabs.

1. **The head names the model**: a 40px ink tile with the maker's initial,
   the model name with its 在售 / 未上架 badge, and maker · family · release
   date under it. Previous, next, copy name and close sit on the right.
2. **资料 reads as an overview**: four figures in 20px (上下文, 最大输出,
   知识截止, 官方价), each badged when it did not come from models.dev
   (本站改过, 推断, 待确认) and captioned with the upstream value when the sync
   disagrees; then rows of pills for 输入 → 输出 and 能力, unsupported
   parameters as struck, dashed pills, and the official cache price.
   编辑资料 replaces the body with the form (board Model C): sections
   规格, 输入与输出, 能力, 接口 (read-only; endpoint paths folded), 上架,
   名称与厂商 and 介绍 on the left; on the right the model as the 模型 page
   shows it (the same facts component) and 本次修改, one line per changed
   field. One footer: 删除模型 on the left, 取消 and 保存 N 处修改 on the
   right. Everything saves in one request.
3. **线路 are cards side by side**, two to a row (one on a narrow screen):
   the lane name with 跟随官方价 or 固定价, input, output and margin in 28px,
   a caption with cache prices, the pool's cost and the ratio, then the
   sources: name and state on one line, 48 hours of thin bars with role,
   weight and latency under it. 改价 opens the card's price and weights in
   place, with 取消 / 保存 at its foot; saving still shows the before/after
   confirmation.
4. **The dialog has one height** from the first frame. It opens from the
   ledger row at once; the lane cards are grey blocks until prices load.
5. The dialog never explains itself in sentences: figures, badges and labels
   only.

## Locked page-UX revision (2026-10-14)

Five shared rules, then the pages built on them.

1. **One page head.** Every console page's title sits at the same place —
   12px / 16px insets inside the content column (290px from the window edge
   at 1440), 10px under the top bar (86px), set as in
   [Console type](#console-type-2026-11-21) at `--lazu-console-heading-size`. Actions align to the title's bottom and
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

## Locked wallet (2026-11-19)

The wallet has two peer views on the page head's underline strip: 充值 and
账单. Both read from the authoritative wallet ledger and the account's own
top-up orders.

1. **充值 is two cards side by side** (stacked on a phone): a 360px balance
   card (the balance as the one large figure, a 余额偏低 warning badge when it
   is under the account's low-balance threshold, and the account's own
   low-balance mail switch at its foot) and the amount card: a 3 × 2 grid of
   preset tiles with 自定义 last, the selected tile outlined in ink, the
   custom amount field, the methods, and a pay button that names the amount
   ("前往支付 $20.00") and opens checkout directly; there is no confirmation
   step. A deployment without a payment method shows the contact slot in the
   amount card's place.
2. **账单 lists months, not days.** The balance and 导出 CSV head the view,
   then a 每月消耗 bar card for the last six months, a month table (入账 ·
   消耗 · 月末结余, the current month badged 本月) and the 充值记录 for the
   same six months. Rows open their month. 充值记录 rows open in place onto
   their numbers and documents; 历史记录 opens all of them in a 780px dialog.
3. **A month** shows 期初 · 入账 · 消耗 · 结余, one bar per day (days still
   to come are a faint stub), and a day table that starts with ten rows and
   a 显示全部 link. Today's row is muted. Its 充值记录 covers only that month.
4. **A day opens as a centered 560px dialog** (a full-height sheet on a
   phone): the date, its request count and spend in the header, one row per
   model, and a footer with the link to that day's request log and
   前一天 / 后一天. A failed usage read shows "—", never zero.
5. The month, the day and an opened receipt live in the URL
   (`?tab=bill&month=YYYY-MM&day=YYYY-MM-DD`, `top_up_id`), so the back button
   and a shared link return to the same view.
6. **Type.** The wallet is set in the sans face throughout, the page title
   and the one large balance figure included; money uses tabular figures.
   Mono is kept for identifiers (充值编号, 发票编号) and the daily chart's axis.

## Locked Console and Docs refinement (2026-09-06 / 09-09 / 09-20)

Consolidates the September 2026 review (`console-review`), UI audit fixes, and
top-bar decisions into one console contract.

### Controls, dialogs, and credentials (2026-09-06)

- **Logout confirmation popover**: shared by the desktop account menu and mobile
  sidebar (`CompactConfirmation`): 210px wide, 14px radius, 28px text actions
  with 8px corners and a danger-soft confirmation action.
- **Dialogs and controls**: control geometry travels into portalled dialogs;
  page layout, heading, and table styles stay scoped to their page. Native
  button resets must not erase shared Select or date-picker chrome. All dialogs
  support outside click and Escape (cancelling rather than submitting);
  in-flight irreversible requests retain busy protection.
- **Inline Key reveal and copy**: Keys use a dedicated fixed-width inline field
  with prefix/suffix masking, reveal, and copy (no separate reveal modal). Fetch
  secrets on explicit interaction only, restricted to the owner for unshared
  customer Keys and administrators for upstream Keys; never include full secrets
  in list responses, persisted client caches, or logs. Unrecoverable legacy
  digest-only Keys state that limitation without inventing a value. Shared
  project Key permissions follow [team-projects.md](team-projects.md).
- **Operational surfaces**: Usage exposes requests and user consumption as
  primary views (monthly reports and files secondary); Users replaces account
  metric filters with one concern filter alongside search, tier, identity,
  status and sorting; Model operations separates base lane prices/sources from
  targeted discounts with change previews; Search services shows masked
  upstream credentials and real last business use (provider tests do not count;
  unknown dates stay unknown).

### Top bar and members empty state (2026-09-20)

Selected directions: **T4** (top bar in the main column, then page head, then
tabs), **X1** (project switcher as first crumb), **V** (build number in help
menu's last line), search in bar dropped, **E2** (members empty state uses the
real members table with one owner row, one inline invite row, and one greyed
placeholder row).

- **Desktop top bar (52px, inside main column)**: no background or bottom rule
  (page head owns the horizontal rule). Left: project switcher (20px tile, name,
  chevron) `›` sidebar group name. Right: 32px icon buttons (10px radius, no
  border) for announcements (6px evidence dot when unread), theme, language,
  help, 1px divider, 28px round avatar. Menu items warm routes on hover/focus
  and hide when their destination is hidden.
- **Mobile drawer exception**: the bar is desktop-only; on phones the drawer
  retains project switcher and logout, and mobile top chrome holds version,
  theme, and language.
- **Members empty state & invite**: `.pj-table` with owner row, inline invite
  row (address, role, cap `$50 / $100 / $500` plus project default if custom,
  disabled until project settings arrive), and one greyed seat row. Later
  invitations open a 440px single-column dialog (address, role, cap, full-width
  send button, one consequence line; no eyebrow, no second column, no cancel
  button beside send).

### UI audit fixes (2026-09-09)

1. **Model directory (`/models`, `/console/models`) — A (one hero price per card)**:
   one price leads the card at display size; its lane row drops the duplicate;
   output-only lanes lead with output; badges are exception-only (unavailable,
   every open lane down, or newly listed); context length rounds to one decimal
   (`1M`, never `1000k`).
2. **Legal documents (`/privacy`, `/terms`) — B (chapter rail)**: title,
   breadcrumb, and published date move into a page head; chapters get a
   scroll-following right rail and mono enumerators; in-page links preserve the
   URL locale prefix.
3. **Console overview (`/console`) — A (de-carded KPI strip)**: single hairline
   strip; no standing status panel; inline notice links to failed requests only
   above error-rate threshold.
4. **Wallet (`/console/wallet`) — A**: opens on the user's last checkout amount
   (never deployment minimum); a created checkout is not a completed payment.
5. **Key inspector — D (answer in the row)**: `/console/key` redirects to the
   Keys table where per-Key usage expands in place (queried by immutable public
   token ID and authenticated owner); public `/key` stays for Key Pass users.
6. **Platform settings (`/console/setting`) — D (one group at a time)**: one
   panel mounts at a time; save bar scopes to the visible group; switching away
   from unsaved drafts guards with a save/discard/stay modal (locked while save
   is in flight); long template fields preview in one line and edit in a modal.
   (Settings search box S4 remains deferred.)

## Locked usage and logs · M3 + L1 + S1 (2026-09-29)

Route: `/console/usage`. Inherits [ui-system.md](ui-system.md) and this console
contract.

### List (L1)

1. **The lane is part of the model.** Lane rides in the model cell as `稳定`
   (ink dot) or `Agent` (ring dot, evidence colour); no 分组 column (`group` is
   `direct` / `cheap`).
2. **Only tag what is known.** Non-lane rows and pre-routing refusals get no
   tag. `改走稳定` (warning tint, hover hint) marks requests served on stable
   instead of the Key's requested lane (`other.lane_requested`).
3. **Status is a dot and a word.** 成功 / 失败; failed rows are not painted.
   Other record kinds keep their type chip.
4. **Two merged columns:** `输入 → 输出` (18.4K → 1.2K, Latin K/M) and
   `用时 · 首字` (6.8s · 0.9s). No 详情 button column; row click opens detail
   and the time cell is the keyboard handle with `aria-expanded`.
5. **Summary strip before filters:** 花费 · 请求 · RPM · TPM (pending until
   query answers).
6. **Filters:** `线路` switch (全部线路 / 稳定 / Agent) + model search;
   operator columns (用户, 通道, 重试) appear in platform view only.
7. **Amounts read to four decimals** (`$0.0213`); under a tenth of a cent keeps
   six decimals so it never rounds up.

### Detail (M3) & F2 ledger

Expands inline under its row on the selected tint without repeating model, lane,
or time (deep links off the page are the exception):

- **Left (220px):** 实扣 (32px mono), then S1 (`官方价 ~~$x~~` and
  `省 $y（z%）` in success green).
- **Middle:** two aligned bars — 时间 (首字 neutral ≥ 64px, then 生成 with
  tok/s and total) and 费用 (one unlabelled segment per billed line in ledger
  swatch colours).
- **Right (380px, F2 ledger):** one header `数量 × 单价 = 金额`, then one row
  per billed line (swatch, name, right-aligned mono 62 / 72 / 70px) in order:
  输入, 图片输入, 缓存读取, 缓存写入 · 5 分钟, 缓存写入 · 1 小时, 音频输入, 输出,
  图片输出, 音频输出, per-call and tool lines, 最低计费. Whole-order discount or
  surcharge (`折扣 6 折 · −40% · −$x` green / `加价 20%` amber) sits between
  小计 and ink rule + 实扣 (14px/600). Tags (`长上下文`, `×2` peak) ride on
  their lines with one-line foot reason.
- **Facts line:** 推理强度, 接口, IP, and request ID (`X-Request-Id`,
  searchable) with 复制.
- **Failed requests** lead with 发生了什么, whether anything was charged, then
  upstream message.
- **Platform view only:** 通道, retry chain, upstream model.
- **No raw-data toggle, for anyone.**

### Official price (S1) rules

1. **Snapshot at billing, never computed on display** (`quote.official.total_microusd`
   in `line_items`; pre-2026-09-29 rows show no comparison).
2. **Only input and output have list prices** (`models.official_*_price`);
   other lines count at charged price so saving is never overstated.
3. **No comparison** for long-context tiers, minimum charges, per-call models,
   or lines missing a list price.
4. **Say nothing when there is no saving:** under 1% shows no line (never
   `省 0%`, never negative).
5. **Never repeat saving on list rows.**
