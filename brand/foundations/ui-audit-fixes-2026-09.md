# UI audit fixes — September 2026

Status: LOCKED by user approval on 2026-09-09.

Source: `archive:brand/explorations/ui-audit-2026-09/` (a verdict table; the screenshot
snapshot itself is in archive history at 903dcda) found twelve pages "小修即可".
`archive:brand/explorations/ui-audit-fixes-2026-09/` grouped them into six root causes and
put four options against each; `archive:brand/explorations/team-console-2026-09/` drew the
selected pages at full size with their interaction states. The user selected one
option per group. This document is the production authority for those six; the
explorations remain decision history.

## Selected directions

| # | Surface | Direction |
| --- | --- | --- |
| 01 | Model directory (`/models` and `/console/models`) | **A** — one hero price per card |
| 02 | Legal documents (`/privacy`, `/terms`) | **B** — reading layout with a chapter rail |
| 03 | Console overview (`/console`) | **A** — de-carded KPI strip |
| 04 | Wallet (`/console/wallet`) | **A** — the top-up as a readable equation |
| 05 | Key inspector (`/console/key`) | **D** — retire the page, answer in the row |
| 06 | Platform settings (`/console/setting`) | **D** — one group at a time |

## Scope amendment

`console-review-2026-09.md` excluded the model directory from the 2026-09-06
review. This decision, three days later, brings that one page back into scope
for direction 01 only. Nothing else in that document changes, and the exclusion
still stands for the public homepage, login and registration.

## What each direction fixes

**01 · Model directory.** Every card read equally flat because the price sat at
12px beside the tags. One price leads the card at display size, its lane row
drops the duplicate, and the remaining lanes stay as rows. A lane priced only on
output leads with output rather than showing no price. Badges are exception-only:
unavailable, every open lane measurably down, or newly listed. Context length
rounds to one decimal (`1M`, never `1000k`).

**02 · Legal documents.** Title, breadcrumb and published date move into a page
head; the body drops the duplicates. Chapters get a contents list, a right-hand
rail that follows the scroll position, and the document's own enumerators as a
mono numeral beside each heading. Every in-page destination keeps the locale
prefix the URL carries, because that prefix is what asserts the language.

**03 · Console overview.** KPIs lose their cards and become one hairline strip.
The standing status panel stays absent per the 2026-07-29 incident treatment;
above the error-rate threshold an inline notice links to the failed requests.
Studio keeps a link here: the locked navigation treats it as a satellite reached
from the dashboard, and the console shell renders no top navigation, so this is
its only entry point.

**04 · Wallet.** The top-up reads as an equation — amount, rate, balance after —
rather than a form with a total appended. The page opens on the amount the reader
last took to checkout, never on the deployment minimum. A created checkout is not
a completed payment, and the stored value is named for what it is.

**05 · Key inspector.** Every Key in the console now has an owner and a row, so
"how much has this Key used" is a question about one row. It opens as an in-place
read-only expansion per `yunmu-console.md` rule 7. `/console/key` redirects to
that table. The public `/key` inspector stays, and a Key Pass — someone holding a
key without a console account — is sent there instead, because the Keys table
does not admit that scope.

Per-Key spend is queried by immutable public token ID and checked against the
authenticated owner. Cache entries include that owner and token ID. The request
log link uses the existing name filter and explicitly labels that broader scope.

**06 · Platform settings.** The five groups become real panels: one mounts at a
time, and the save bar scopes to the group in front of the reader. Switching away
from unsaved drafts is guarded by a modal offering save, discard or stay; that
modal stays locked while the save is in flight. Long template fields preview in
one line and edit in a modal, per the centred-editing rule.

## Not implemented

The settings search box drawn as artboard S4 needs the five schema modules
hoisted out of their lazy chunks. It is deliberately deferred, not dropped.
