---
name: lazu-api
description: "Use this skill when working with the Lazu API or this repository's unified AI gateway: discover token-scoped models via `/api/models/catalog`, choose the correct Lazu endpoint, or update Lazu API docs/examples/skills."
---

# Lazu API

Use this skill when a task involves integrating with Lazu, choosing the right model endpoint, or keeping Lazu API guidance in sync.

## Recommended Workflow

1. Start with `GET /api/models/catalog` using `Authorization: Bearer <api-key>`.
2. Read each model's `modality`, `supported_endpoint_types`, `supported_endpoints`, `default_endpoint_type`, `pricing`, `usage_capabilities`, `parameters`, and `example`.
   `supported_endpoints[].mode` distinguishes a native endpoint from a lossless bridge.
3. Prefer the model's `default_endpoint_type` unless the user explicitly wants Anthropic-native or Gemini-native syntax.
4. Call the concrete path from `supported_endpoints`.
5. Use `GET /v1/models` only when a tool specifically needs the OpenAI-compatible model list.

## Authentication

- Default auth header: `Authorization: Bearer <api-key>`
- Anthropic-native requests may also use `x-api-key` plus `anthropic-version`
- Gemini-native requests may use `?key=<api-key>` or `x-goog-api-key`

## Endpoint Map

- OpenAI chat: `POST /v1/chat/completions`
- OpenAI responses: `POST /v1/responses`
- Anthropic messages: `POST /v1/messages`
- Gemini generate content: `POST /v1beta/models/{model}:generateContent`
- Embeddings: `POST /v1/embeddings`
- Image generation: `POST /v1/images/generations`
- Video generation: `POST /v1/videos`
- Rerank: `POST /v1/rerank`
- Decisions: `POST /v1/decisions` (non-streaming; OpenAI uses `model: "gpt-6-luna"`, `input`, ordered `questions[]`; Jev/Clef retain `state` and keyed questions)
- Request usage detail: `GET /api/usage/requests/{request_id}`

The gateway may serve Chat and Responses through a lossless bridge when an
accessible upstream only exposes the other OpenAI wire protocol. For a
Responses-to-Chat stateless bridge, omitted `store` is accepted and returns
`X-Lazu-Warning: stateless_bridge`; `store: false` is accepted without the
warning; explicit `store: true` returns `400 protocol_bridge_unsupported`.
Unknown or stateful fields that cannot be preserved are rejected before the
upstream call rather than silently dropped.

## Decisions

OpenAI Decisions uses the official wire model `gpt-6-luna` at `/v1/decisions`; its Chat/Responses endpoints remain separate. The catalog item `gpt-6-luna-decisions` carries endpoint-specific routing, permissions and prices, with `request_model: "gpt-6-luna"`; the alias is also accepted as a compatibility request name. New clients send `{"model":"gpt-6-luna","input":"The screen is broken.","questions":[{"name":"damaged","type":"predicate","instructions":"Is the screen broken?"}]}`. Answers are ordered arrays; unnamed questions and typed boolean/string choice values are preserved. Supplied names must be unique in Lazu's initial release. Score values use the probability-weighted zero-based rubric index; confidence is a separate fact. Per-question refusals are valid billed results, not a reason to retry the whole request. Usage retains input/output/cache details; check the catalog's active prices rather than borrowing the text model's price.

The OpenAI alias initially limits input plus questions to 8192 estimated text tokens, 64 questions, 4 inline PNG/JPEG/WebP images and 2–10 score levels. These are Lazu limits. Native input permits only user messages and inline image parts with low/high/auto detail; no tools, streaming, safety_identifier, regional endpoints or native question_generation. The alias also supports the legacy state/keyed-questions bridge; never mix input and state. Jev and Clef keep their existing semantics and limits.

For the legacy state/keyed-questions format, read `decision_capabilities` from the catalog. Use explicit `questions` for stable semantics. `question_generation: {enabled: true, model: "gpt-6-luna"}` optionally compiles `model + state` when the platform allows it. It is disabled by default. Generation and native decisions have separate request IDs, usage and charges; the native `usage` excludes generation. Unusable generated questions are refunded. If native evaluation fails after valid generation, reuse `error.details.questions` to avoid generating and paying again. Images must be inline PNG/JPEG/WebP data URLs; remote URLs, uploaded file IDs, audio, video and streaming are rejected. Clef currently enforces a conservative 2000 UTF-8 byte state guard in addition to estimated token limits.

## Seedream

Seedream generation and reference editing both use JSON `POST /v1/images/generations`; `image` accepts URLs/data URLs or an array. Do not use multipart edits. Lazu currently supports non-streaming responses. Read `parameters` for sizes and per-model limits. Lite/4.5 native batch generation uses `sequential_image_generation: "auto"` and `sequential_image_generation_options.max_images`; references plus max outputs must not exceed 15. Pro/Flash produce one ordinary output, or accept `layer_decomposition: true` with exactly one reference (prompt optional, size defaults to auto). Preserve each layer's size, z_index, bounding_box, name and description.

Read `pricing.image_pricing` for per-output, pixel-tier, layer and additional-reference prices. Only successful outputs are billed, using each returned size; decomposition includes the base image. Reported output tokens are not an extra token charge. The gateway reserves maximum possible output cost (up to 17 images for layers) and settles actual successful images. Compare `usage.generated_images`, `usage.cost` and the request usage record. Studio supports ordinary generation and reference edits; use the API for native batches and layers.

## Usage Metadata

- Use `usage_capabilities.reported_dimensions` to know which usage and cache dimensions may appear for a model.
- Use `usage_capabilities.billable_dimensions` to know which dimensions can affect the active catalog price.
- In OpenAI-compatible responses, cache reads appear as `usage.prompt_tokens_details.cached_tokens` or `usage.input_tokens_details.cached_tokens`.
- Cache writes may appear as `cache_write_tokens`, `cache_write_5m_tokens`, or `cache_write_1h_tokens` when the upstream reports them.
- DeepSeek-style cache misses may appear as `cache_miss_tokens`.
- For reconciliation, use `GET /api/usage/requests/{request_id}` with the same API key that made the request.

- New text request details may include `usage.measurement.input` and `.output`: `provider`, `local_estimate`, or `unavailable`. Older records may omit it. Never treat an admission reservation as provider usage.
- For stateless tool continuation, retain every output item (including reasoning) and append function results using the original `call_id`; see the complete Responses example in Docs. Do not replay automatically after output is received.
- `request_body_too_large` is 413; `gateway_overloaded` is local 503 and does not trigger provider failover.

## Errors

- Error objects carry `error.code` (what happened, branch on it), `error.type` (the protocol-specific error type), `error.lazu` (`LZ-####`, quote to support), and `error.docs`. The HTTP status is always real. Never parse `error.message`.
- For OpenAI-compatible error responses, read the correlation ID from `error.request_id`. For `/v1/messages` (Anthropic), read the top-level `request_id` beside `error`; there is no `error.request_id`. The `X-Lazu-Request-Id` response header also carries the ID. Retain it in logs and support reports.
- 401 has three causes: `invalid_api_key`, `token_expired`, `token_disabled`. 403 `ip_not_allowed` / `model_not_allowed` / `token_vendor_denied` are key scope, not auth.
- 429 `insufficient_quota` and the `*_budget_exceeded` codes need money or a budget change; retrying will not help. Only `rate_limit_error` codes should be retried after `Retry-After`.
- `no_available_channel` (503) means the model exists but no route can serve it now; `model_not_found` (404) means the name is not in the catalog. `context_length_exceeded` (400) needs a shorter input or a longer-context model.
- Upstream failures whose cause is on Lazu's side (`channel_config_invalid`, `upstream_auth_failed`, `upstream_billing_failed`) answer "temporarily unavailable" with a 502/503; retry later or pick another model.
- `/v1/messages` uses Anthropic's error shape and types (`insufficient_quota` → `billing_error`).
- Full list: https://lazu.ai/docs/errors (anchors `#lz-####`).

## Repo Sync

When editing this repository, keep these sources aligned:

- `apps/docs/pages/`
- `packages/contracts/openapi/lazu-api-reference.v1.yaml`
- `.codex/skills/lazu-api/SKILL.md`

Do not document `/api/pricing` as the primary agent discovery flow when token-scoped access matters; use `/api/models/catalog` first.
