# Console and Docs refinement — September 2026

Status: LOCKED by user approval on 2026-09-06.

Selected reference: `archive:brand/explorations/ui-redesign-2026-09/`, final HTML and the
`compact-review.css` refinements. This decision supersedes conflicting earlier
review notes in that exploration. Retain Yunmu colors, typography and tokens.

Scope: overview, usage/logs, wallet, tier pricing, API Keys, users, model
operations, search services, personal settings, platform settings, channel
health, announcements, Docs reading and API reference layouts.

Excluded: model directory, public homepage, login and registration. The model
directory exclusion was lifted on 2026-09-09 for the UI-audit fix 01 only; see
`ui-audit-fixes-2026-09.md`. The other exclusions stand. Preserve
sidebar version/announcement entry, theme and language controls — superseded on
2026-09-20 by `console-top-bar-2026-09.md`, which moves those controls into a
console top bar on desktop and keeps this arrangement in the mobile drawer.
Logout uses an adjacent confirmation popover. The compact recipe refined in
PR #247 is shared by the desktop account menu and mobile sidebar: 210px wide,
14px surface radius, 28px text actions with 8px corners and a danger-soft
confirmation action. Its geometry belongs to `CompactConfirmation` and the
canonical confirmation tokens, independent of the popup primitive.

Review control appearance follows React composition into portalled dialogs
and drawers. Only the control recipe travels; page layout, heading, and table
styles stay scoped to their page. Native button resets must not erase shared
Select or date-picker chrome, including outside review pages.

All dialogs support outside click and Escape;
closing cancels rather than submitting. In-flight irreversible requests retain
busy protection until their outcome is known.

Keys use a dedicated, fixed-width inline field with prefix/suffix masking,
reveal and copy; no separate reveal modal. Fetch secrets on explicit interaction,
restricted to the owner for unshared customer Keys and administrators for upstream Keys.
Never include full secrets in list responses, persisted client caches or logs.
Unrecoverable legacy digest-only Keys state that limitation without inventing a
value. This explicitly supersedes the former owner one-time-only reveal UX;
platform operators still cannot read another user's Key via this capability.

For shared projects, the approved 2026-09-10
[team-projects amendment](team-projects-2026-09.md) defines the actor/target
matrix: owner can manage admin/member personal Keys, admin can manage member
Keys only; active stewards have narrowly scoped service-Key access. Every
cross-person reveal is audited and queues a notification. Project roles never
come from platform roles. The same foundation registers the project-member
master-detail exception and its unsaved-edit/confirmation behavior.

Usage exposes requests and user consumption as primary views; monthly reports
and files move to secondary navigation. Keep original request fields in receipts.
Users replace account metric filters with one concern filter alongside search,
tier, identity, status and sorting. Model operations separate base lane prices /
sources from targeted discounts, previewing changed values before saving.
Search services show masked upstream credentials and real last business use;
provider tests do not count as business use. Unknown dates stay unknown.

Keep original operational settings and financial dimensions. Compact title,
actions, filter and summary layout must make body content primary and wrap at
narrow widths. Remove prototype-only state pickers and unnecessary refresh
controls; preserve retry and genuine synchronization operations. Docs use a
left navigation, readable central column, right anchors and integrated code tools.

HTML data is synthetic. Production always binds existing authorized API data,
permissions, error/loading states, and actual mutations. HTML click handlers are
interaction references, never production business logic.
