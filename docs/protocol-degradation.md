# Endpoint gaps and bounded degradation

Status: Accepted (2026-10-20). Amends the "lossy 显式放行 → 不做" row of
[protocol-conversion-architecture.md](./protocol-conversion-architecture.md):
the operator decided that a short, named, tested list of stand-ins is better
than a 400 for the two structured-output cases below. Everything else that
document says about refusing still holds.

## The problem it solves

A client sends a Chat Completions request with `response_format: json_schema`
to a channel whose Chat API takes `text | json_object` only (DeepSeek). Before
this change the planner saw a native Chat endpoint, sent the request as it
was, and the vendor answered 400. The model catalog could not express the
difference: `supports_response_schema` is one flag per model, and DeepSeek's
Responses API *does* take the schema.

## Two mechanisms, in order

### 1. Endpoint gaps: route around a known gap

`CapabilitySnapshot.EndpointGaps` lists, per native endpoint, the features the
dialect's endpoint does not carry although its other endpoint does. It is a
fact about the vendor's wire, declared once per dialect in
`packages/domain/providercap` (`dialectEndpointGaps`), never about a model:

| Dialect | Endpoint | Gap |
| --- | --- | --- |
| `deepseek` | Chat Completions | `output.json_schema`, `output.json_schema.strict` |

The planner (`textgen.PlanAttempt`) keeps native as the default. When the
request uses a feature the native endpoint lacks and the other endpoint
carries it, it plans the existing one-hop bridge to that endpoint, with every
bridge rule unchanged (reasoning state across tool calls still refuses the
Chat→Responses bridge). The plan carries the warning
`endpoint_bridge:<feature>`. If no lossless route exists, the request is
refused with the feature ID and field path, as before.

Gaps are part of the snapshot `Revision`. `FeaturesOn(endpoint)` and
`UnsupportedEverywhere(required)` are the two readers.

### 2. Bounded degradation: stand in for what no endpoint carries

`textgen.Degradations` (`internal/protocol/textgen/degrade.go`) is the
registry. A rule stands in for exactly one feature, and only when the channel
is **known** not to carry it anywhere:

- the operator denied it on the channel (`capabilities.supports_json_schema:
  false`, the new overlay flag), or
- it is gapped on every native endpoint the channel has.

Unknown is not unsupported: a model row that simply lacks a flag never
triggers a rule. The provider's own refusal reaches the client, as before.

| ID | Stands in for | What the client gets |
| --- | --- | --- |
| `json_schema_to_json_object` | `output.json_schema` | `json_object`, with the schema stated at the end of the instructions; only when the channel takes `json_object` |
| `json_schema_to_instruction` | `output.json_schema` | no format field; the schema stated in the instructions |

Rules run in `runtime.PrepareAttempt`, before the upstream call, on a clone
of the immutable request; the caller's request never changes. Because they
run pre-flight, no retry-after-bytes question arises. Each applied rule adds
`degraded:<id>` to the plan's warnings, which reach the client in
`X-Lazu-Warning` (every warning, comma separated), the request detail
(`protocol_warnings`) and the hot-path counter `protocol.degraded.<id>`.

## What this is not

- **Not learning.** Nothing here reads a vendor's 400 and remembers it.
  magpie's `unfit` memo does that for one user; on a multi-tenant gateway a
  memo keyed on provider+model that any tenant's request can poison is not a
  compatibility mechanism. A new gap or rule is a reviewed change with a
  source.
- **Not a general lossy mode.** Adding a rule means: one feature, one ID, a
  reason, a source link, a case in `TestDegradationRegistryIsComplete`
  (which fails on a rule without one), and a row in this table.
- **Not a catalog change.** The catalog's endpoint projection still answers
  "which endpoints serve this model"; gaps are feature-level within an
  endpoint and do not alter it.

## Adding a gap or a rule

1. Gap: add the dialect → endpoint → features row to `dialectEndpointGaps`
   with the vendor document and date in the comment; a planner test showing
   the routed and the refused case.
2. Rule: add a `DegradationRule` to `Degradations`, a case to
   `TestDegradationRegistryIsComplete`, a behaviour test, and a row above.
3. Say in the PR which warning string the client will see.

Rule: AGENTS.md, Protocol Layer 5.
