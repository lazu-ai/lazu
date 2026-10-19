# Reviewed model release dates

`model-release-dates-2026-09-28.json` records the reviewed corrections for the
24 offered models whose effective release dates were missing on September 28.
It is an operational evidence manifest, not an automatic seed or runtime override.

Use the publisher's first public release date for the exact identifier. A public
preview or paid-plan release counts; a private evaluation does not. Distinct
preview/GA identifiers have distinct dates. A later update behind the same alias,
open-weight publication, regional API availability, or Lazu listing does not
reset the release date. Use the calendar date printed by the publisher.

All 24 production records had `release_date` marked `legacy`; their models.dev
matches had already produced pending `first_run` reviews. The dates were therefore
intentionally omitted from public pricing and sorted last. Several upstream
proposals were later update dates or disagreed with original announcements.

The reviewed correction was applied in one transaction after saving the old date
and source map locally. Each row was checked against its complete prior source
map, date, model name and unlocked status before writing. Only `release_date` and
its provenance were changed; the 24 corresponding pending date proposals were
resolved. Corrected dates are `manual` so daily sync cannot overwrite them.
Caches were invalidated through the existing model-info API. Public pricing and
authenticated channel health then returned the corrected dates and order; all 59
offered models had nonempty effective release dates.

For future corrections, use Model operations → Updates → missing release dates,
open the facts panel and save the verified date, or **Keep** a date already known
to be correct. Keep now confirms its provenance as manual, transactionally with
the review decision; a stale review cannot confirm a changed date. Never batch
accept upstream dates solely to clear the missing list. The manifest's source
URLs and notes explain the original-release versus later-update distinctions.
