# Errors

Every failure Lazu reports has one identity, defined once in the error
catalog (`internal/types/error_catalog*.go`), and leaves the server through
one writer. This page is the contract for adding and reporting errors.

## Anatomy

| Field | Meaning | Changes when |
| --- | --- | --- |
| `lazu` | Lazu error number, `LZ-####`. The thousands digit is the family. Shown in the console, docs and support tickets. | Never. |
| `type` | What the client should do next. OpenAI / Anthropic aligned. Drives SDK behaviour and console presentation. | Never. |
| `code` | What happened, snake_case. Programs branch on it; the console localizes by it. | Never for a documented code. |
| `message` | English sentence for developers and logs, with concrete values. | Freely. No program may parse it. |
| `details` | Structured values (`field`, `model`, `retry_after_seconds`, ...). Fill the message and the console copy. | Additive. |
| `request_id` | The request this failure belongs to. Also `X-Lazu-Request-Id`. | — |
| `docs` | `https://lazu.ai/docs/errors#lz-####`. | — |

Every error response also carries `X-Lazu-Error: LZ-####`. Rows marked
`NoClientRetry` (wallet and budgets) add `x-should-retry: false`, which the
Anthropic and OpenAI SDKs obey ahead of the status code.

### Families

| Range | Family | Who can fix it |
| --- | --- | --- |
| LZ-1xxx | The request itself | The caller, by changing the request |
| LZ-20xx / 21xx / 23xx | API keys, console sessions, access | The caller, with credentials or an admin |
| LZ-3xxx | Wallet and budgets | Whoever controls the money |
| LZ-4xxx | Models and routes | Choose another model, or wait |
| LZ-5xxx | Rate and capacity | Wait |
| LZ-6xxx | Upstream providers | Wait; operators see the cause |
| LZ-7xxx | Console domains (users, keys, top-ups, channels, settings, pricing, projects, files) | The console user |
| LZ-9xxx | Internal | Nobody on the client side; report with the request ID |

### Types

`invalid_request_error`, `authentication_error`, `permission_error`,
`not_found_error`, `rate_limit_error`, `api_error`, `overloaded_error`,
`api_connection_error`, `timeout_error`, `content_policy_violation_error`,
`insufficient_quota`, and `conflict_error` (console only). The Anthropic
dialect maps them to Anthropic's own types (`insufficient_quota` becomes
`billing_error`, `timeout_error` becomes `api_error`).

## Envelopes

Two envelopes exist. Both use the real HTTP status. A row's
`MessagesStatus` replaces that status on `/v1/messages` where Anthropic and
OpenAI disagree: an empty wallet is 429 `insufficient_quota` on the OpenAI
dialect (OpenAI's pair, which Codex treats as final) and 402 `billing_error`
on the Anthropic dialect.

```jsonc
// /v1 (OpenAI dialect): OpenAI's four fields, then Lazu's additions
{"error": {"message": "...", "type": "insufficient_quota", "param": null, "code": "insufficient_quota",
           "lazu": "LZ-3101", "docs": "https://lazu.ai/docs/errors#lz-3101",
           "request_id": "req_...", "details": {}}}

// /v1/messages (Anthropic dialect)
{"type": "error", "error": {"type": "billing_error", "message": "...", "code": "insufficient_quota",
                            "lazu": "LZ-3101", "docs": "..."}, "request_id": "req_..."}

// /api (console)
{"success": false, "error": {"message": "...", "type": "not_found_error", "code": "topup_order_not_found",
                             "lazu": "LZ-7101", "docs": "...", "request_id": "req_...", "details": {"order_id": 8841}}}
```

Streams end with the protocol's own terminal frame carrying the same object.

## Audience

Each public row names who can act on it. `customer` rows explain the cause.
`operator` rows (a broken channel key, a provider account out of credit, a
missing price) tell customers only that the model is temporarily unavailable;
the cause is logged with the request ID and shown on operator pages.
`internal` rows never describe their cause outside the server.

Operator details are omitted from gateway responses and customer console
responses. Authenticated platform administrators on `/api/` retain structured
operator validation details (such as a lane, list index or limit); an admin's
`/v1` API key does not grant diagnostic access. Internal details stay private
for every role.

## Internal rows

The gateway keeps finer distinctions than clients need: retry classes, the
channel breaker and operator logs tell `channel:param_override_invalid` apart
from `channel:header_override_invalid`. Such rows set `Public` to the row
clients see (`channel_config_invalid`). Their `HTTPStatus`, `Retry` and
`FallbackCat` keep driving server-side decisions; the wire shows only the
public row. `WireStatus` overrides a misleading runtime status, e.g. a
provider's 401 caused by Lazu's own key is written as 502.

## Writing errors

Go:

```go
// model / service: return a coded error; keep the cause for logs
return types.Err(types.ErrorCodeTopupOrderNotFound, types.D{"order_id": id})
return types.WrapErr(err, types.ErrorCodeInternalError)

// controller: one exit
httperr.Write(c, err)                        // console envelope
httperr.Invalid(c, "email")                  // invalid_request on one field
writeControlPlaneAPIError(c, err)            // /v1 control-plane routes
abortWithCode(c, types.ErrorCodeModelNotAllowed, types.D{"model": m}) // relay middleware
```

Anything that is not a catalog error is written as `internal_error`; its text
is logged, never sent. Success responses keep `{"success": true, "data": ...}`
and carry no prose: the console writes its own confirmation copy.

Web:

```ts
const data = unwrap<Token[]>(await API.get('/api/token/'));  // throws LazuApiError
showError(error);                // pass the error, never error.message
<ErrorState error={error} />     // copy for the code + LZ number and request ID
```

The console never shows `error.message`. `errorMessage(error)` picks the copy
for `code` from the `errors` i18n namespace, then the copy for `type`.

## Adding an error

1. Add the constant and catalog row in the domain's
   `internal/types/error_catalog_<domain>.go` (gateway and shared rows live in
   `error_catalog.go`). Pick the next free number in the family.
2. Add the console copy for the code in all four languages under
   `apps/web/src/i18n/locales/errors/<lang>/<domain>.json` (a new domain file
   is also listed in `locales/errors/index.ts`). Copy states the fact, then
   the impact, then one action.
3. Map upstream text in `internal/relay/errmap/` if a provider produces it.
4. Run `go test ./internal/types/` (catalog shape, unique numbers, locale
   coverage), `python3 ops/scripts/check_error_outlets.py`, and
   `go run ./ops/tools/errdocs` to refresh the docs reference.

## Current failure boundaries

| Origin | Example | Client action | Internal failover |
| --- | --- | --- | --- |
| Request | 413 `request_body_too_large` / `invalid_request_error` | Reduce the body; decompressed and unknown-length bodies use the same rule | Never |
| Identity / quota | Invalid key, forbidden scope, insufficient funds | Fix credentials, permissions or budget | Never |
| Local gateway capacity | 503 `gateway_overloaded` / `overloaded_error` | Retry later with backoff | Never; no provider was selected/executed |
| Provider capacity / transport | `upstream_overloaded`, `upstream_timeout`, `upstream_network_error` | Replay only when safe | Only eligible compatible same-model attempts before content is delivered |
| Protocol bridge | `protocol_bridge_unsupported` | Use a supported/native route or change the request | Never by silently deleting fields |
| Partial stream | Error/timeout/disconnect after output | Retain partial result, inspect request detail | Do not replay automatically |
| Media job | Terminal job error | Inspect job and billing state | Submission and terminal success are distinct |
| Media URL fetch | 400 `invalid_request` naming the fetch policy (private address, port, scheme) | Host the file somewhere public, or have the operator add the source to `fetch_setting.private_allow_list`; see [DEPLOYMENT.md](DEPLOYMENT.md#fetching-client-supplied-media-urls) | Never; the refusal is the gateway's own and is not channel evidence |

The catalog's `Retry` describes a client-facing retry category. `FallbackCat`,
request state and `IsSkipRetryError` constrain the gateway's attempt policy;
these are not interchangeable booleans. A client-explicit alternative-model
list is an opt-in compatibility feature, not permission for silent model swaps.
A same-model failover attempt must compile again from the original request.

SSE cannot change the already-sent HTTP status. Emit a protocol error or failed
terminal event, retain the request ID and incomplete outcome, and settle only
supported usage evidence. Never emit a success terminator after a known failure.
See [billing-contract.md](billing-contract.md) for money handling.

## Health and capacity

Customer request outcomes, provider attempts, probes and asynchronous job
terminal outcomes are separate evidence. A failed attempt followed by success
is one successful customer request but still a failed provider attempt. Local
validation, auth, quota and capacity rejections are not upstream health failures.
Probe-only health must not invent request throughput or completed job success.

### Stream start deadline

A streamed request waits at most 90 seconds from arrival, across all of its
attempts, for an upstream to start: its response headers, then its first data
event. Past that the attempt ends with `upstream_timeout` and is not retried.
Two things change the wait after an attempt's headers have arrived:

| Case | Wait for the first data event |
| --- | --- |
| The source sets `first_event_timeout_seconds` (console: First event wait) | That many seconds from the headers, longer or shorter than the deadline (0 to 3600) |
| The request has already switched sources once | Up to `STREAMING_TIMEOUT`, because nothing can follow this attempt |
| Otherwise | The 90-second request deadline |

Set the source value for an upstream that answers headers at once and then
reasons before its first event, or lower it for one that tends to stall after
its headers. The wait for headers is always the request deadline. Keep-alive
comments do not extend either wait.

### Source account failures

An upstream that reports our own account there cannot pay
(`upstream_billing_failed`, usually a 402) refuses every request until someone
tops it up. With automatic disabling on, such a failure disables the source (or,
on a multi-key source, the key) like a rejected key does, so requests stop
spending an attempt and their one source switch on it. A source with automatic
disabling turned off stays in rotation. The scheduled source test re-enables an
automatically disabled source once a test request succeeds, when automatic
enabling is on.

The existing performance guard samples host CPU/memory and the selected data
filesystem every 10 seconds; it is not a container quota or latency SLA. Current
default thresholds are CPU 90%, memory 90%, disk 95%; active-connection rejection
is disabled unless configured. Keep it as a configurable capacity safeguard
while measuring deployment-specific false rejection and sustained load. Do not
claim a host percentage alone is an accurate per-process resource limit.

Body limits and cancellation cleanup remain mandatory. Non-critical comparison
metrics must not trigger additional media downloads or make a successful request
fail. Accounting persistence and tenant checks are not optional telemetry.
