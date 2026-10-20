# Wire dialect patch layer

Package: `internal/relay/wirepatch`. Rule: [AGENTS.md › Engineering Rules › Protocol Layer, rule 6](../AGENTS.md#protocol-layer). Design baseline: [protocol-conversion-architecture.md › 渠道 ParamOverride 分为两层](./protocol-conversion-architecture.md#渠道-paramoverride-分为两层).

## What it is

A vendor sometimes rejects or mangles a body that is valid for the protocol it
claims to speak. That branch is the shape of the world, not of our code
(Principle 3), so it cannot be deleted, but it can be confined: it lives in
exactly one layer, under a name, with a source link and a regression test.
That layer is the wire dialect patch layer. Codecs convert protocols; channel
adaptors do transport; neither contains a vendor conditional.

## Position in the pipeline

```text
plan attempt
  → codec compiles the outbound body
  → RemoveDisabledFields
  → operator ParamOverride            (ApplyParamOverride, raw JSON)
  → wire dialect patch                (wirepatch.Apply(dialect, body))   ← here
  → outbound validation / transport
```

The hook is one marked line on each outbound path (a handler with two
compile paths has two), directly after ParamOverride where there is one, so
it runs exactly once per attempt and also covers bytes the operator
introduced:

- `internal/relay/channel/openai/native_request.go` (`ConvertNativeRequest`,
  the byte-preserving path every OpenAI-family channel takes; its result is
  cached per channel in `NativeBodyCache`, so retries on the same channel do
  not re-run it)
- `internal/relay/compatible_handler.go` (DTO conversion branch)
- `internal/relay/responses_handler.go` (DTO conversion branch, and the
  `bridgeToChat` branch a Responses client takes to a Chat-only vendor such
  as DeepSeek)
- `internal/relay/claude_handler.go`
- `internal/relay/gemini_handler.go`

Pass-through bodies are not patched: that setting means "send my bytes". The
`bridgeToResponses` branch in the compatible handler (a Chat client to a
Responses-only vendor) applies no ParamOverride today and carries no hook; no
registered patch targets a Responses-only dialect yet, and adding one there is
a one-line change at the same position.

The dialect is resolved by `wirepatch.ResolveDialect(info)`: the plan's
`UpstreamProtocol.Dialect` when an attempt was planned, otherwise the
channel's configured `protocol_dialect` normalised for its API type
(`providercap.NormalizeDialect`). A relay info without a channel yields "" and
the layer is a no-op.

## What a patch may do

- Fix provider wire shape: an escape the vendor's validator rejects, a field
  name the vendor reads under another key, a URL dialect.
- Return the body **unchanged, as the same slice**, when nothing matches, and
  otherwise splice only the affected bytes (gjson offsets), never re-encode
  the whole document. The bytes outside the quirk, and with them the prompt
  cache prefix, must stay identical.

A patch must not change the model, add a feature the plan did not approve, or
move tool-execution responsibility. Those are planning decisions and belong
before the body is compiled.

## Adding a quirk

A quirk is a registered `Patch` with all of:

| Field | Meaning |
| --- | --- |
| `ID` | stable snake_case name; the PR, the test and any request detail refer to it |
| `Dialects` | the `providercap.Dialect*` values it runs for |
| `Reason` | one line: what the vendor rejects or mangles without it |
| `Source` | a link to the evidence: vendor doc, issue, request ID, reference implementation |
| `Apply` | `func(body []byte, ctx Context) []byte` |

plus one regression test named after the ID in `internal/relay/wirepatch`.
`TestRegistryEntriesAreNamedJustifiedAndSourced` fails on an entry that is
missing any field or targets a dialect `NormalizeDialect` can never produce
(such a patch would never run, which Principle 7 forbids). If the vendor has no
dialect yet, add one: the constant in `packages/domain/providercap/profile.go`,
`NormalizeDialect`, the Chat-bridge list in `internal/protocol/textgen/plan.go`,
the legacy inference in `internal/model/protocol_capability_migration.go`, and
the console's dialect select and URL inference under `apps/web/src`.

## Registered quirks

| ID | Dialects | Reason | Source |
| --- | --- | --- | --- |
| `deepseek_tool_patterns` | `deepseek` | DeepSeek's schema validator rejects the ECMAScript null escape `\0` inside tool JSON-schema `pattern` fields ("Invalid schema for function ... is not a 'regex'"); `\u0000` is accepted. Rewritten in place under `tools[].function.parameters` (Chat) and `tools[].parameters` (Responses), following `properties`, `$defs`, `items` and the combinators; descriptions, enum values and anything outside `tools` are untouched. | [magpie `deepseek_schema.go`](https://github.com/yetone/magpie/blob/main/internal/gateway/deepseek_schema.go) (MIT) |
