# Billing Contract

This document defines how Lazu charges quota for relay requests.

## Reservation and usage

Admission, reservation and settlement are separate values:

- `RelayInfo.PromptTokens` is the quick admission estimate. It is never evidence of provider usage and must not be overwritten during settlement.
- Reservation quotes input and output at their respective prices. Explicit output caps include all requested completions; generative requests with omitted caps use the existing pre-consumption baseline. Embedding (including native Gemini single/batch), rerank and moderation requests do not reserve output tokens by default. This is a reservation estimate, not an enforced maximum.
- Every prepaid account reserves funds transactionally, including high balances and unlimited API keys. Unlimited keys remove only the key's own cap. Account and spend-budget checks still apply.
- Actual provider counters take priority. Missing counters may use locally counted request/output data, with separate input/output provenance. Disabled counting, unavailable media dimensions and unsupported local rules remain `unavailable`; they do not fall back to the reservation.
- New text `usage_fact.measurement` and request-detail `usage.measurement` identify `provider`, `local_estimate` or `unavailable` for each side. Historical rows may omit this field; absence is not proof of provider measurement. The existing `source` field identifies the request origin and is unrelated to measurement.

Counters without an explicit measurement source remain `unavailable`, even when positive. Adapters mark provider counters when decoding upstream usage, before normalization or local fallback. Synthetic image counts and fixed image-token estimates are `local_estimate`. These internal markers cannot be supplied through provider wire JSON and do not change the billed quantities or historical rows.

A provider total alone does not establish an input/output split. Gemini usage may retain that total for diagnostics, but must not assign all of it to the output dimension when input counting is unavailable. A failed native Chat stream with only metadata, heartbeats or malformed events and no observed billable usage refunds its reservation, including per-call pricing. Receiving the first upstream event is not evidence of a billable result. A network failure before any stream event or downstream write can use the existing same-model failover policy while retaining the reservation for the next attempt.

Image admission reads at most a bounded inline header (64 KiB decoded, with an encoded-byte limit too), never remote URLs or decoded pixel buffers. Known image rules use dimensions and detail; unknown dimensions use the corresponding model reserve. Unknown model families retain a documented heuristic (2048 image tokens; K2.6: 4096), so they do **not** provide a hard cost bound. Local image counting uses the same sizing rules and only shrinks images. It cannot settle an unknown remote image as if it had been measured. Rules are estimates checked against the [provider guide](https://developers.openai.com/api/docs/guides/images-vision); they do not alter model capability or admit/reject a model.

A hard spending guarantee additionally requires enforced output limits and concurrency control. Reservations alone cannot promise that an uncapped request will never cost more than the reserved amount. Insufficient funds at final settlement remain recoverable accounting work; do not silently overdraft or fabricate a successful charge.

## Charge rules

| Evidence at termination | Settlement rule |
| --- | --- |
| Provider usage available | Quote the reported dimensions using the effective model/lane price and pricing version. |
| Observable text/tool output, usage incomplete | Preserve provider dimensions; estimate missing supported dimensions, retain measurement provenance and mark the interrupted request incomplete. |
| No useful output and no billable usage on failure/cancellation | Release the reservation; no invocation fee. |
| Missing usage and no observed output supplied by the handler | Do not substitute admission or the reserved amount, including per-call pricing. |
| Same-model failover | Keep provider attempts separate; reconcile one customer reservation against the final billable result. Never replay after content has been delivered. |
| Existing asynchronous media job | Submission is not completion. Settle the verified terminal job from its billing snapshot; failure/refund and repeated polls must be idempotent. |

A terminal successful text response can have no generated text while still reporting input usage. Do not confuse that with an unexecuted failure. Input cache dimensions retain their provider-specific inclusion/exclusion semantics.

## Recovery

`apps/worker/main.go` registers both recovery tasks when the database is ready. The failure reconciler defaults to enabled (`SETTLEMENT_FAILURE_RECONCILER_ENABLED=true`, 30 seconds); durable outbox recording and its reconciler are optional (`SETTLEMENT_OUTBOX_ENABLED=false`, 15-second interval when enabled). Outbox being off does not mean failure recovery is absent.

Replays check the unique wallet event before charging, reopen stale claims, and refund the account even when its API key has been removed. An insufficient-balance recovery goes to manual review rather than overdrafting. Verify the actual worker, configuration, pending count and oldest pending record in the deployment before asserting operational recovery is healthy. Local replay tests do not establish production configuration or backlog state.

No cleanup or counter-rule change recalculates historical bills automatically.

## Discounts

A discount (or, at a negative pct, a surcharge) applies to the whole order: every line item, including cache, audio, image, per-request and per-second lines.

The effective charge is:

```text
subtotal = sum(all line items)
discount = subtotal * tier_discount_pct
total = subtotal - discount
```

## Operational Notes

The relay path may pre-consume quota before the upstream call finishes. On non-billable failures the post-call handler must refund the pre-consumed amount. Consumption logs should include `amount_microusd`, token counts, pricing version, and line items so billing can be audited without reconstructing provider responses.
