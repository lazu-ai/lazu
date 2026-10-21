# Model operations ledger — locked

The selected model operations direction is the ledger and inspector in
`archive:brand/explorations/model-ops-rules-2026-09/DECIDED.md`: model ledger R1–R5,
source ledger R6/R6b, and discount matrix E1. This foundation promotes that
recorded selection into the production contract; other explorations remain history.

Model and source editing use one centred dialog over the ledger (880px,
shared Modal, tabs inside, fixed head and foot, body scrolls); see the
2026-10-04 refinement. The former 560px side inspector exception is
withdrawn. Confirmations and pricing previews use shared Modal; tab
navigation uses shared Tabs.

Preserve ledger row height and selection tint without rails or left borders.
On narrow screens allow horizontal scrolling rather than wrapping data into
unlabelled rows. Tiers and lanes are registry driven. Sensitive pricing rules
and their editing controls are root-only.

## Approved refinement — 2026-09-22

Model operations defaults to the publicly offered catalog; imported metadata
remains accessible through the all-status filter. The navigation model count
comes from the same public catalog on every tab. Discount rules are not model
counts and are not shown as a navigation badge.

The discount matrix uses continuous page scrolling with opaque sticky headers
and a default-discount row, without a bounded inner vertical scrollport or
pagination footer. Search leads the toolbar at 420px on desktop and fills the
row on mobile. Customer tiers remain selectable; tier creation/editing and
its inspector are removed from this page. Saving prices retains a review
confirmation, and switching tiers protects unsaved edits.

## Approved refinement — 2026-09-28 (model order and model facts)

Every catalog surface (model plaza, model status, connect dialog, this ledger)
reads one order: the operator's maker order (厂商顺序, an Options row), then
newest release first, flagship before light on the same day. The server sends
lists already in that order; pages group by maker and never re-sort. The
ledger folds rows under collapsible maker headers and drops the vendor column
(the vendor a model was imported from stays on the panel's 信息 page); a
release-date column carries each date's source (models.dev / 手填 / 待确认).

Model facts come from models.dev (github.com/anomalyco/models.dev, MIT) once
a day. Empty facts are filled; disagreements on facts that change a bill or a
request (context, max output, official price) are only proposed, on the
资料更新 page; a model can be locked so the sync only proposes. The panel
gains a 资料 page listing each fact with its source.

Long-context pricing is edited per lane on the panel's 定价 page, under one
switch and one shared threshold. The plaza card shows the price most requests
pay and a dashed-footnote line for the long-context price; the detail modal
shows both tiers per lane with the rule in one paragraph; a consume log billed
at the long-context price carries a neutral 长上下文 tag and a one-line
explanation, never a warning tint.

## Approved refinement — 2026-10-04 (two pages, one dialog, ratio pricing)

模型运营 is two pages, 模型 and 来源. 资料更新 became the 模型 page's
「待处理」 view; 专属折扣 moved beside 用户 (`/console/price-plans`) because
it prices customers, not models.

A model or a source opens one centred dialog. The model dialog has two tabs:
定价与来源 and 资料 (the former 资料 and 信息 pages, saved by one button).
The source dialog has 凭据 / 模型 / 线路; adding a source walks the same three
as steps. Header previous/next buttons stay; keyboard row paging is gone.

Vocabulary: 厂商 (who made the model), 来源 (one upstream credential), 线路
(稳定 / Agent 专线). 「档位」「渠道」 are not used in operator copy.

Each lane is priced as a percent of the official list price. A lane carries a
default ratio (稳定 100%, Agent 专线 50% at launch); a model follows it or sets its
own; an operator may pin single prices (input, output, cache) — rare, behind
「改单项」. When the official price moves, following prices move with it and
the move is listed under 待处理. Prices that do not follow stay fixed and are
marked 固定 in the ledger. Billing reads only stored prices: a ratio is turned
into a new price version when it, the pin set or the official price changes.
