# Team projects — locked direction (2026-09-10)

Status: **LOCKED by user approval on 2026-09-10**, including the contract-review
amendments and the 2026-09-10 revision below. This document selects the
"团队管理" milestone deferred by
PR #217 and supersedes the role model and backend backlog in
`archive:brand/explorations/team-console-2026-09/index.html`. Within this milestone it
also refines older foundations' customer-Key access and member-detail rules.
The selected artboards live in `archive:brand/explorations/team-workspace-2026-09/`
(build with `python3 build.py`) and on
`archive:brand/canvases.md`.
If an artboard conflicts with this amended foundation, this foundation wins;
update its affected states during frontend implementation.

## 2026-09-10 revision

Four rules were simplified after the first contract review. They are fixed
product rules, never shown in project settings and never user-configurable:

1. There is **one kind of Key**. It belongs to a project and to one member —
   its owner — is charged to the project balance, counts against that member's
   monthly budget, and may carry a lower per-Key monthly cap (`cap_microusd`).
   There are no service Keys, no steward and no hand-over.
2. A member who is removed, or who leaves, has **all** their Keys in that
   project stopped immediately. There is no grace period and no leave policy;
   the 10-minute removal undo is unchanged.
3. Invitation links are valid for **3 days**.
4. At most **200** non-deleted Keys per person per project. Disabled and
   expired Keys count; soft-deleted and internal/system Keys do not. This is a
   runaway-script guard, not a product rule (raised from 10 on 2026-09-29): one
   Key per tool is good practice and spend is already bounded per member.

The rest of this document is written to that revision.

## One concept: the project

1. Every user owns exactly one **default project**, created at signup and
   named after the user. Alone, the console is identical to today: balance,
   Keys, usage, wallet. No project vocabulary appears, except one quiet
   "邀请别人一起用" card on the overview.
2. The first invitation names the project and turns the default project into
   a **shared project in place**. Balance, Keys and history do not move. The
   inviter remains owner of this same project; upgrading creates no second
   project. An owner may still accept invitations into other projects.
3. Belonging to two or more projects is the only condition for showing the
   **project switcher**, irrespective of role. It reuses `ConsoleScopeBar`
   workspace scopes. Switching changes the console's view, never a Key's
   charging target. `is_default` means "owned by this user", not "unshared";
   `upgraded_at` distinguishes an unshared project from a shared one.
4. A **Key belongs to the project it was created in**. Project membership
   grants no platform role. The existing platform customer-management page
   (`/console/organizations`) stays; members and budgets get a separate
   project page and route.

## Money and resource identity

- A project's balance **is the owner's balance** (`User.quota`). Pricing
  tier and discounts are the owner's. The person initiating a request is
  independent of its payer: keep authentication/resource identity separate
  from `payer_user_id` and `project_id` throughout the request lifecycle.
- **Member budget** is a monthly cap, not a reservation: accepting an
  invitation charges nothing; requests do. Budgets reset on the 1st at 00:00
  UTC, unused budget does not carry over. A cap lowered below month-to-date
  usage blocks new requests immediately. Existing accepted requests still
  settle or refund against their original project, payer and actor.
- **Project monthly cap** is optional and off by default. Allocation (sum of
  member caps vs. project cap) is informative. Existing platform-wide budgets
  remain an additional constraint, not a replacement for project budgets.
- The six project/Key rejection reasons are `member_budget_exceeded`,
  `key_budget_exceeded` (Key quota or token-scope budget),
  `project_budget_exceeded`, `model_not_allowed`, `insufficient_quota`
  (project balance), and `project_access_suspended`. Each has a distinct
  explanation and action. These extend the existing error catalog; invalid
  credentials, rate limits and platform-budget errors keep their own codes.
  Identifiable rejected requests are logged once without becoming consumption.
- **Every Key has a human owner.** Its actor is that member, its payer is the
  project owner, and tasks and files belong to the project and the originating
  Key. User 0 is never an account or an authorization shortcut. A Key never
  inherits the payer's personal or platform privileges — a member's Key in a
  project owned by an administrator gets no platform powers. Key rotation keeps
  the same logical Key: it does not reattribute historical usage or reset
  budgets. Rows written before this revision that still carry the retired
  `is_service` flag keep working as ordinary Keys owned by their `user_id`; a
  row with `user_id = 0` is attributed to the project owner.
- **Ownership transfer is not in v1.** Dissolving returns the same project
  to its unshared default state, retaining the owner's balance and history.
  All other members lose access, invitations are revoked, and their Keys stop
  immediately. The owner's own Keys retain their existing state. Dissolution
  does not forgive pending charges or reactivate Keys if the project is shared
  again.

## Permissions: actor, target and action

All administrative actions below require active project membership. A
platform role alone grants none of these permissions. "Member" as a target
means the ordinary member role, not any membership row.

| Action / target | Owner | Admin | Member |
| --- | --- | --- | --- |
| Top up | Yes | Only if `who_can_topup=owner_admin` | No |
| Read project top-ups and invoices | Yes | Yes | No |
| Enter Stripe portal / manage saved billing details | Yes | No | No |
| Invite, resend, revoke an invitation for member | Yes | Yes | No |
| Invite, resend, revoke an invitation for admin | Yes | No | No |
| Promote / demote admin | Yes, never owner | No | No |
| Change another person's budget | Admin or member | Member only | No |
| Change own member budget | No (owner manages project cap) | No | No |
| Suspend / restore / remove / undo-remove | Admin or member | Member only | No |
| See Key metadata | All project Keys | All project Keys | Own only |
| Reveal / rotate / mutate another person's Key | Admin or member | Member only | No |
| Reveal / rotate / mutate own Key | Yes | Yes | Yes |
| Edit project settings / dissolve | Yes | No | No |
| Request / withdraw a budget increase | No | No | Own request only |
| Approve / reject member budget request | Yes | Yes, ordinary member only | No |

Resource reads (tasks/files/logs) are project-scoped: active members read
their own; owner/admin may read within the project, never across it. A Key
may access resources of its own logical Key only. Rotation keeps that logical
Key identity; it does not create a fresh budget or a new grant to unrelated
project resources. Reading or managing someone's Key never grants the right to
delete the resources it produced — only the original actor may do that.

Reveal is fetched only on explicit interaction, never listed, cached or
logged in plaintext. Every reveal of another person's Key writes an audit
row and queues a notification to that person. Receipts distinguish
notification queued from delivered. Keys in an **unshared** default project
are owner-only; shared default projects follow the matrix. Admins can see
owner/admin Key metadata but cannot reveal or mutate those Keys.

## Access lifecycle

| State / event | Console access | Existing Key calls |
| --- | --- | --- |
| Active | Per role | Subject to all Key, budget and account checks |
| Suspended | Own usage/logs/budget history read-only; no Key list or mutations | Immediately rejected |
| Removed, or left voluntarily | Revoked immediately | Immediately rejected |
| Project dissolved / Key manually disabled or deleted | Per resulting project state | Affected Keys stop immediately |

Removal and self-leave stop **all** of that person's Keys in that project at
once. There is no grace period, no eligibility window and no leave policy to
configure; the same rule applies however the membership ended. Account
suspension, project revocation, explicit Key revocation and all budgets
continue to apply independently. A removed member cannot create, reveal,
rotate or reactivate Keys. Owner self-leave is unavailable.

Removal has a 10-minute undo window. Record the removal operation and affected
Keys' previous states; undo restores only changes caused by that operation,
never subsequent revocations/deletions or naturally expired Keys. It restores
the previous membership state, not automatically active. Rejoining creates
no automatic revival of old Keys. Revocation must invalidate cached access
across API instances. Accepted jobs retain their original billing snapshot
and can finish settlement/refund after removal, suspension or dissolution.

## Project defaults and fixed v1 values

Owner-editable project settings: new-member monthly budget (initially $100),
warning threshold (initially 80%), optional monthly project cap (initially
unset), and who may top up (initially owner + admin). Budget defaults apply to
future invitations; editing defaults does not silently rewrite existing members
or pending invites. An invitation snapshots its budget and warning threshold
when issued.

Fixed values, none of them shown in settings or configurable: at most 200
non-deleted Keys per user per project (disabled and expired Keys still count;
soft-deleted and internal/system Keys do not), invite validity 3 days, reveal
re-mask after 30 s, removal undo 10 minutes, immediate stop of a departed
member's Keys, and monthly reset on the 1st at 00:00 UTC. Preserve legacy Keys
above the limit; block new creation until below it. Key rotation defaults to
immediate expiry of the old secret, with an explicit 24-hour overlap option
sharing the same budget and logical Key.

## Interaction rules (extend `console-review-2026-09.md`)

- Escape and outside click cancel a dialog before submission. While a request
  is in flight, keep busy/close protection and prevent duplicate submission.
  A timeout means outcome unknown, not proof that nothing changed or was paid.
- Irreversible actions use a centered modal with a checkbox gate; dissolution
  uses a typed-name gate. Reversible actions (suspend access, disable a Key,
  revoke an invitation) use adjacent confirmation.
- Budget edits are inline (1–3 fields). Role/access changes execute after
  their own confirmation, never sit in a save bar.
- **Member edits use a small centered dialog** (2026-09-28, replaces the
  master-detail "third drawer exception"): a row's 「编辑」 opens a 520px
  dialog with three fields only — monthly budget, allowed models and the
  reasoning-effort cap — saved together. Role, suspension and removal stay in
  the row's ··· menu with their own confirmations. There is no member detail
  page; `/console/project/members/:userId` redirects to the list with
  `?edit=<id>`. Keys and usage are read on the Keys page and the usage log.
- Errors explain what happened and one action; reason codes are for filters.
  Project-wide "需要处理" counts come from backend aggregates, never sums of
  currently loaded member/Key/log pages.

## Invoices

Stripe-paid top-ups produce an overseas Invoice and receipt, issued by DUBRIO
LLC in English. No domestic 增值税发票 workflow or self-built billing form.
The existing owner's Stripe Customer is adopted by their default project
without creating a duplicate; a user joining another project never brings
that Customer with them. A top-up records both beneficiary project and the
logged-in person paying; billing details describe the project's invoice
recipient and use the details Stripe collects in Checkout. As approved on
2026-09-26, `billing_address_collection=auto`: do not require a full address for
every top-up. Stripe requests an address when the payment method or tax rules
need it; otherwise an invoice may omit the recipient address. Only the owner
may manage saved billing details through the portal; an admin payment must not
silently overwrite them.
Historical invoices retain their original Bill to snapshot.

Balance credit and Invoice readiness are independent, idempotent states:
webhooks may be duplicated or arrive in either order. An Invoice failure must
not delay or repeat credit. The console stores invoice identifiers, Bill to
snapshots and download links, and shows explicit pending/failed/ready states.
Paid top-ups send one Lazu e-mail — credit added, with a link to the Stripe
hosted Invoice page that carries both the Invoice and the Receipt (the Invoice
PDF is not handed out; see `payment-emails.md` § 6) — as locked in
[payment-emails](payment-emails.md) (2026-09-23, one e-mail since 2026-09-27); historical receipts remain
available. The project wallet also keeps Invoice and receipt links. Mainland-China
customers receive the same overseas Invoice
and a fixed explanation that Lazu cannot issue 增值税发票. Tax wording must match
confirmed billing configuration; do not hard-code reverse charge for every
buyer or infer a tax conclusion from whether Stripe Tax is enabled.

## Team delivery refinements (2026-09-17)

- During account-allowlist rollout, both inviter and recipient must have project
  access. Create/resend rejects an unavailable recipient before upgrading the
  project or queuing mail. Global rollout still permits invitations to new users.
  This does not grant project or platform access through an invitation alone.
- A successful invitation acceptance remains successful if refreshing the console
  session fails. Opening the project is a separate, retryable action.
- Shared project Keys reuse the locked API Key connection dialog. Creation opens
  it with the new secret; existing Keys use project-scoped reveal and connect-only
  updates, preserving member budgets, version checks and notification receipts.
- Payment-return wallet links select the credited project explicitly. Failed
  reads are retryable errors, never empty member/Key inventories or zero balances.


## Team experience refinement (2026-09-22)

Approved by the owner after review of the local team-experience HTML and its
shortened copy. This refinement preserves the existing permission and budget
contracts.

- Invitation and joined-success mail use the identity-email warm-paper card,
  canonical Seal lockup, a concise title, a compact facts table and one ink CTA.
  Invitation mail states its actual absolute deadline with an explicit time
  zone. The console updates relative time from the effective server deadline
  every minute and at expiry, including when the tab regains focus.
- Project settings use full-width information and budget cards in the shared
  label/value/explanation row system. Fixed member/Key rules are omitted.
  The full-width danger section follows the settings; its typed-name
  confirmation and accurate consequences remain intact.
- Team Key creation composes the same form as personal creation: name, amount,
  expiry, vendor lanes and collapsed advanced settings. Team context adds the
  project, member, payer, monthly budget and remaining budget. The amount maps
  to the Key monthly cap, not lifetime quota. Keep the existing 10-Key rule and
  secret reveal/connection flow. Team creation has one Key per submission.
- Copy is short: omit redundant introductory and explanatory paragraphs.
  Retain financial constraints, unknown/error states and destructive effects.
  Supported top-up choices remain owner and owner/admin.
- The approved refinement uses a 16px Key modal, 14px settings cards and
  8px Key input corners, defined by the team refinement tokens in `ui.css`.
  Existing console navigation and real catalog labels remain canonical.


## Reasoning-effort cap and team refinement (2026-09-28)

Approved by the owner on the canvas
`archive:brand/canvases.md` (sources:
`archive:brand/explorations/thinking-depth-2026-09/`, `python3 build.py`). Direction
**A · 刻度**, drawn as **A2 「填线」**; the other directions (B 深度条, C 三档,
D 按供应商, A1 分段, A3 下拉) are decision history only.

**The rule.** An owner or admin caps the highest reasoning effort ("思考深度")
a member's Keys may use, and each Key may lower it further.

- One ladder: `minimal < low < medium < high < xhigh < max`. A cap is one
  rung; it and everything below it are allowed. A cap at `max` is no cap and
  is stored as `""`. `none` sits below the ladder and is never capped;
  anything above `max` (Codex `ultra`) is capped to `max`.
- Layers only narrow: the project default seeds new members; the member cap
  applies to all their Keys; a Key cap may only be lower. The effective cap is
  the lower of Key and member. Permissions follow the member-management matrix
  (admins cap ordinary members only; the owner caps admins and members).
- **Over-cap requests are lowered whenever the model supports a level within the cap.** The gateway rewrites the
  final upstream request body after model mapping, protocol conversion and channel overrides (`reasoning_effort`,
  `reasoning.effort`, `output_config.effort`, Gemini `thinkingLevel`), clamps
  thinking-token budgets (`budget_tokens`, `thinkingBudget`, Qwen
  `thinking_budget`) to the level's threshold, lowers effort suffixes on model
  names, and turns thinking off on on/off-only models when the cap is below
  `high`. Each vendor gets its highest supported level at or below the cap
  (DeepSeek / Kimi / GLM have no `medium`: `medium` becomes `low`; Gemini
  tops out at `high`). The response carries `X-Lazu-Effort-Applied`; the usage
  log records `effort_requested`, `effort_applied`, `effort_cap_source` and
  `effort_clamped`, counted in the console as 「被降 N 次」 beside 「被拒」.
- Member allowed models are an allowlist on the membership; each Key's model
  scope narrows it further. They are enforced wherever the Key's own
  allowlist is (routing, `/v1/models`, media jobs).

**The control.** `components/ui/effort-cap.tsx`: the six English words in
mono in a row, an ink line filled to the cap with a knob under the chosen
word; words below the cap in ink, above it faint; words above a ceiling (the
member cap, for a Key) are disabled. The words are never translated. Tables
show `≤ medium` in a mono badge, or 「全部深度」 when uncapped. The control
appears in the Key create/edit form after 线路, in the member edit dialog and
in project settings' 新成员默认 card.

**Team pages.**

- 成员与预算: the allocation bar lives inside the ledger's 「预算分配」 cell in
  the evidence palette (ink for a used-up budget, no red series); budget cells
  are ink figures over a thin bar, and only a used-up budget says 「用完」;
  row actions stay at reduced opacity until the row is hovered; a 思考深度
  column shows each member's cap; owner and admin role badges use the
  evidence tone.
- API Keys (team): 有效限制 is a line of words, not a filled chip, with a
  second line of 「被拒 N 次 · 被降 N 次」 links; the 范围 column carries the
  model scope and the effective effort cap side by side.
- 项目设置 uses the personal-settings cards (`SettingsCard` / `SettingsRow` in
  `components/ui/settings-card.tsx`): one card per group with a title and a
  one-line description, rows of `label + explanation | value + action`. Values
  are facts; each row saves on its own through a small dialog or menu, and the
  effort cap saves on change. There is no page-wide save bar. The danger zone
  is its own card with the danger button.

### Review clarification (2026-09-27)

- A cap is strict. If a model has no supported level at or below it and cannot
  disable reasoning, reject before contacting the provider with an explicit
  error asking the caller to change model or raise the cap. Never snap upward
  or send an unsupported off value. This exception replaces “never rejected”.
- Model-specific subsets take precedence over vendor-wide ladders (for example
  Gemini 3 Pro supports low/high, while 3.1 Pro also supports medium).
- Missing effort fields are bounded in the final outgoing protocol. Explicit
  off values and known defaults below the cap stay below it. Per-message effort
  overrides must also respect the cap.
- New-member defaults are captured when an invitation is issued. Pending and
  resent invitations retain that snapshot even if project defaults change.


## Invitation funnel and budget notices (2026-10-14)

Approved by the owner on the canvas
`archive:brand/canvases.md` after a production check
found two shared projects out of 198 and an invitation that expired unanswered.

**Invitations.**

- A signed-out visitor sees one primary action, 「用 <invited email> 继续」. The
  invitation page shows the invited address. Sign-in opens with that address
  fixed, sends the code at once and names the project being joined; a verified
  code returns to the invitation, which accepts on arrival.
- A live invitation lets the address it names create an account while
  registration is otherwise closed. The code proves the address, so the new
  account's e-mail counts as verified.
- Signed in on another verified address: show both addresses side by side; the
  primary action signs out and continues with the invited one. The invitation's
  address is never changed from this page.
- Signed in without a verified address: send a code to the invited address on
  the page, bind it to the account, then accept.
- The invitee is reminded once 24 hours before expiry; the sender is told once
  when it expires unanswered. Both are cancelled by acceptance, revocation,
  resend or dissolution.

**Budget notices.** A member hears at their warning line and when the budget
is used up. Whoever may change that budget (the owner and admins for an
ordinary member, the owner for an admin) hears only when it is used up; a
warning to managers would repeat for every member every month. The owner hears
about the optional project cap at both points. "Used up" is either spend
crossing the cap at settlement or the first refusal for the budget, because
admission refuses the request that would cross it. Each notice is sent once
per budget, month and limit, so a raised budget counts again. The team's
「需要处理」 also lists members past their warning line, except those already
listed as refused today.
