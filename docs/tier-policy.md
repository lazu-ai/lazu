# Tier Policy — dormant implementation reference

Product status (2026-09-07): the hosted tier and rate-limit program is not in
use. The details below describe retained implementation, not a published plan
or proof of production configuration. Do not expose these defaults as customer
entitlements. Enabling the program requires a separate product decision.

Lazu stores the user's billing tier in `users.group`. This field is no longer a customer-selectable routing group.

## Default Thresholds

| Tier  | 30d upgrade spend | 14d downgrade spend | RPM |       TPM |
| ----- | ----------------: | ------------------: | --: | --------: |
| Tier0 |                 - |                   - |  60 |   100,000 |
| Tier1 |               $10 |                  $2 | 120 |   300,000 |
| Tier2 |              $100 |                 $20 | 300 | 1,000,000 |
| Tier3 |            $1,000 |                $200 | 600 | 2,000,000 |

The thresholds live in the `TierThresholds` option as JSON. Values use microUSD for spend thresholds.

## Automatic Adjustment

The worker runs `run-tier-adjust` daily at 04:00 UTC.

Upgrade rules:

- Rolling 30-day spend selects the target tier.
- The daily worker upgrades at most one tier per run.
- Successful top-up can immediately move the user to the tier implied by lifetime credited top-up.

Downgrade rules:

- Rolling 14-day spend at or below the current tier downgrade threshold downgrades one tier.
- Downgrades set a 14-day `tier_downgrade_eligible_at` cooldown.
- `tier_locked=true` skips all automatic adjustment.

## Rate Limits

Tier RPM and TPM extend the existing Redis/model rate-limit middleware. No Postgres hot-path table is used.

- RPM key: `rateLimit:RPM:tier:<tier>:user:<user_id>`
- TPM key: `rateLimit:TPM:tier:<tier>:user:<user_id>`
- A limit value of `0` disables that limit.
- Exceeded limits return `429` with `Retry-After`.

Tier discounts apply to the whole order, every billing dimension included. See [billing-contract.md](./billing-contract.md).
