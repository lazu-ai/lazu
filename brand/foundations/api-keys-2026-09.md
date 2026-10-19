# API Keys page — locked direction (2026-09-16)

Status: **LOCKED by user approval on 2026-09-16.** This document selects
direction **B (table) + E (connect dialog)** out of
`lazu-ops:brand/explorations/api-keys-2026-09/` and the revision boards that followed
it. The exploration keeps the rejected directions and the decision history;
this file is what `apps/web/src/pages/Token/` implements. Where an artboard
and this file disagree, this file wins.

Route: `/console/token`. It inherits
[yunmu-console.md](yunmu-console.md), [ui-system.md](ui-system.md),
[component-contracts.md](component-contracts.md) and the locked row geometry
in [console-review-2026-09.md](console-review-2026-09.md); nothing below
overrides those.

## What the page is for

A Key is only useful inside a tool. The page therefore does two things and
says so in its layout: **copy a Key**, and **put it into an agent**. Quota,
lanes and limits are settings on that, not the subject.

1. **The Key cell is copy-first.** The row shows the masked value and copies
   the whole Key on click. A masked row has to fetch the value first, and a
   browser may by then refuse the clipboard write; the row then shows the Key
   instead of claiming a copy that did not happen. The full value is revealed from the row menu, and
   hides itself again after 60 seconds; a rotation replaces the value and
   says so in a toast.
2. **A Key carries its tool.** `product` stores one connect-tool id and the
   list shows it as a badge, so a person with eight Keys can tell them apart
   without opening anything.
3. **Creation ends in the connect dialog.** Creating a single Key opens
   "接入" straight away with the new value in hand. There is no post-create
   banner: the dialog is the announcement, and a toast confirms the copy.

## The list

- Eight default columns; the column picker persists under a versioned key.
- Filters sit on the **left** of the toolbar, before the count. 创建密钥 is
  the one primary button on the page, 40px, on the right.
- Selecting rows replaces the filter row with a quiet selection bar — count,
  停用, 删除, 取消. Nothing labelled "批量操作" appears; the actions are the
  bar.
- A row action never invents its own geometry: 30px controls, ink foreground,
  per console-review.
- 重新生成 is a row button only for a legacy Key, which has no other way back;
  every Key that allows rotation reaches it from the row menu.
- The tool mark and the dialog's copy control take `--lazu-radius-badge`.
  The boards drew them at 6px and 7px; the page does not keep a private
  radius scale, so they follow the token instead.

## The connect dialog (E)

An 880px centered modal — never a right drawer.

- **Left rail**: 12 tools in four groups (agent, editor, chat, sdk).
- **Controls**: 默认模型, 轻量模型, 线路. Both model fields are the user's to
  set and both offer the same candidates; neither dropdown is narrowed to a
  vendor. What the same-vendor rule still binds is the light model's
  _default_: it opens on one from the main model's vendor, so a Claude agent's
  side tasks stay on Claude unless the user says otherwise, and when that
  vendor offers no lighter model the main model stands in rather than
  something from another vendor. Once the user picks a light model by hand it
  stays put as the main model changes. Choosing across vendors carries no
  warning, badge or explanatory line — the gateway converts the request, and
  saying so in the dialog would be operator detail on a customer surface.
  The field is never called a "槽位".
- **Import row**: the tool's own import path when it has one — CC Switch and
  app deep links are custom schemes only. A Key must never travel in an
  `https://` URL, so a web app gets manual steps instead of a link. The same
  rule governs the list's 对话 action: an administrator's chat template that
  would substitute the Key into an https URL is not offered at all, and one
  that needs no Key opens without spending a reveal.
- **Formats**: one-line installer (`/setup/<tool>`), PowerShell, or the config
  file / manual steps. **No block the user runs ever carries the Key** — not
  as an argument, not as an `export`: a pasted command line is readable in
  `ps` and is written to the shell's history file. The installer asks for the
  Key on the terminal with the echo off; the `export` blocks show a
  placeholder; and the dialog puts a 复制密钥 button beside both.
  `LAZU_API_KEY` in the environment skips the prompt for unattended installs.
  A config file the user edits — settings.json, opencode.json, a .env — and
  the fields typed into a settings window carry the value itself, since
  neither a shell nor a repository ever sees them. Source samples do not: they
  read `LAZU_API_KEY` from the environment, because a pasted sample ends up
  committed. The test suite pins the exact list of blocks allowed to carry a
  Key, so adding a format has to come through that list.
- A member who may not read a Key's value still gets a working dialog: every
  block shows the placeholder and copies, and only the import link and the
  复制密钥 button — the two things that need the value — are disabled.
- A tool is offered only while the deployment can actually serve it: no model
  its protocol can call, or every candidate owned by a vendor this Key blocks,
  and the dialog says so instead of handing out a snippet. This holds for the
  chat apps that choose their own model too — they still need this gateway to
  serve one.
- **Which models are offered**: the pricing catalog decides, never the union
  of model names the enabled channels carry — that union holds ids the catalog
  has dropped, with no price, vendor or protocol behind them. Image, video,
  audio, embedding and rerank models are out; a tool is offered only what its
  format can be served on, read off the catalog's endpoint markers, which is
  how a channel family that cannot bridge the format (xAI for Claude and
  Gemini, anything without Responses for Codex) drops out. The list groups by
  vendor — the tool's own first, then a fixed order — newest release first
  inside each, four before the rest fold away, and a dated or named variant
  folds under the model it varies. The default is the newest flagship the
  leading vendor has, derived from the catalog rather than a written-down
  model list, so a catalog update moves it on its own.
- **Footer**: docs link, 关闭, 复制并完成. Both ways out of the dialog — the
  import button and 复制并完成 — save the two fields it owns, `product` and the
  lane, through the connect-only update path, so a dialog opened against a
  stale row cannot write back an old quota.
- The dialog does not ask the user to confirm the tool or narrow the model
  scope. Those were toggles in an earlier board and are removed.

## Create

One screen, no steps.

1. 名称.
2. **额度** — the prominent block: presets (10 / 50 / 200 / 不限) plus a free
   field. Default 50 USD.
3. 有效期 — a segmented control; a custom choice requires an actual date.
4. **线路** — three vendor rows (OpenAI / Claude / Gemini), each 稳定 or
   Agent 专线. This is a default setting, not an advanced one. A vendor's family is
   resolved from the model catalog's `vendor_id`, not from the model name,
   and the example model beside a row is one that row actually routes.
5. **更多设置** (collapsed) — 模型范围, 速率, 网络, 标签. A validation error
   inside it expands the section rather than failing silently.

There is no tool picker in the create form: the connect dialog opens
afterwards and does that job better.

Everything the model catalog supplies — ids above all — is data, never code:
it is escaped for the shell, JSON, TOML, dotenv line, URL or source file it
lands in, in the console's snippets and in the installers alike. A deployment
can enable any model name under 255 bytes; none of them is refused for its
spelling, and a snippet is something the user pastes into their own terminal.

**模型范围** defaults to all models and otherwise is a searchable picker
grouped by vendor. It offers only models this deployment actually serves, and
only those the chosen tool's protocol can reach.

**标签 (metadata)** is a plain key/value row editor that writes the JSON object
the API stores. The word "JSON" and the brackets do not appear in the UI. It is
optional and the form says what it is for — labels the Key carries into usage
records — in one line.

## Backend contract

- `GET /setup/:tool` and `/setup/:tool.ps1` serve the installers as plain
  text. The base URL is the configured `ServerAddress` — and only when it is
  a plain http(s) origin, since it lands inside a command the user pastes into
  a terminal — except that the
  shipped `http://localhost:3000` default is only trusted for a loopback
  request; otherwise the request's own origin is used and the response is not
  cached. That origin is the one the user typed — the scheme and host-with-
  port the TLS terminator forwards, not the plain-HTTP internal hop — or an
  agent would be told to send its Key in the clear.
- `PUT /api/token/?connect_only=true` writes `product` and `vendor_groups`
  and nothing else — and only those the request actually carries, so setting
  a tool cannot erase a lane.
- The Codex installer writes `LAZU_API_KEY` into every startup file the
  user's shell actually reads — for bash that means the login profile as well
  as `.bashrc`, or an ssh session would start without it.
- `POST /api/token/:id/reveal` and `/rotate` are rate limited per account
  alone. The client IP is not part of the key: a budget that moves with the IP
  is no budget for whoever can change theirs.

## Revision 2026-09-30 — overview, wider editor, two-pane scope

Approved on the canvas "API Key 编辑与总览改版" (boards List, ModalB,
ScopeStates). Where this section and the text above disagree, this section
wins.

- **Overview figures.** Four equal cards sit between the head and the table:
  启用中 (with the total), 本月已用 (this calendar month, every Key), 快用完
  (live Keys past 80% of a finite budget) and 7 天内到期. They read every Key,
  not the page on screen, and show a pending bar until answered.
- **Status is a tab row with counts**, in place of the status select; a status
  nobody is in drops out of the row. The list's status cell is a dot and a
  word, not a coloured pill.
- **One sheet, one edge.** Toolbar, table and pager share a single card with a
  20px inset; toolbar controls are 40px tall. Quota reads used on the left,
  budget on the right, one bar under both (dashed when unlimited). Expiry
  within seven days reads "N 天后". The model-scope cell names a vendor-only
  scope as "{vendor} · 全部" rather than "全部".
- **Edit opens at 880px** with 15px labels and 36–46px controls. Create keeps
  the compact form so it still fits a 1280×720 screen with its button in view.
- **模型范围 is two panes.** Vendors with a tri-state box on the left (the box
  takes a vendor whole, models it ships later included; the name opens it),
  the open vendor's models on the right with an 整家纳入 switch. The section
  head carries a one-line summary ("1 个厂商整家 · 3 个单选模型") and 清空.
  Switched on with nothing ticked cannot be saved, and the picker says so
  before the Save press.

## Revision 2026-10-02 — API addresses, toolbar off the card

Approved on the canvas "API Keys 多接入域名"
(`lazu-ops:brand/canvases.md`). Where this section and
the text above disagree, this section wins.

- **The overview cards are gone.** 启用中 lives in the status tabs; 本月已用
  belongs to 用量与日志. 快用完 and 7 天内到期 become two more status tabs,
  shown only while their count is not zero, and filter the full Key list.
- **The toolbar stands on the page, outside the table card**, 14px above it:
  status tabs on the left; search (名称或尾号), the order (an icon and two
  words, e.g. 最新创建) and 创建密钥 on the right. 创建密钥 no longer sits in
  the page head. The column picker is an icon in the table's own header row,
  over the actions column. A selection replaces the toolbar in the same place.
- **API addresses.** Besides the default address, an administrator registers
  extra ones (平台设置 › 账号与接入 › 接入地址), each with a kind: 全球加速 or
  国内直连. The kind fixes the customer-facing label and its one line; no
  hostname is written into code. A new address is open to nobody; 客户账户 ›
  接入地址 opens it per account. A team Key is checked against the project
  owner, so one switch covers a company.
- **Customers see addresses only once one is opened to them**: a strip under
  the page head, one cell per address — kind (默认 on the default), address
  with a copy button, and its line. Everyone else's page is unchanged. The
  list arrives with `/api/user/self`, so the strip never waits on its own.
- **The gateway enforces it.** A request whose Host is a registered address
  and whose paying account is not opened to it is refused with
  `address_not_enabled` (LZ-2307) before any billing; the default address and
  unregistered hosts are untouched.
