# AGENTS.md

Repo-level context for AI agents (Codex, Claude Code, Cursor, and similar tools) working in this codebase. Read this first, then inspect the relevant code before editing.

## What This Is

Lazu is an open-source OpenAI-compatible AI gateway, operations console, and public documentation site. The product, package namespace, database and service names are all Lazu. Earlier product names are retired from product branding, code identifiers and file paths. Retain legal attribution in NOTICE and the original source copyright headers; the scanner permits only these explicit legal exceptions. CI runs `ops/scripts/check-brand-residue.sh` to enforce this.

See [README.md](./README.md) for the full project structure and setup instructions.

## Principles

Eight principles behind every rule in this file. A rule says how; a principle
says why, and outlives the code the rule was written for. Each one ends with
the question a reviewer asks. A PR that can answer all eight is usually sound;
one that cannot answer one usually is not. Examples of each principle being
broken belong in [Lessons](#lessons), not here, so this section stays stable.

1. **One fact has one source.** Capabilities, prices, the model catalogue,
   brand tokens: each is defined in one place and projected everywhere else.
   Ask: where else could this value be computed from? If there is a second
   place, delete one. (`brand/` is the only design authority; the capability
   snapshot is the only capability authority.)
2. **Do not state what you do not know.** A pending query is not zero, a
   request we cannot translate faithfully is refused with the reason, a
   missing capability flag is unknown, not false. Ask: what does this code do
   when the information is absent? The frontend's Loading rule 1 and the
   protocol layer's refuse-before-lossy rule are the same principle.
3. **Structural complexity goes to zero; essential complexity is isolated and
   named.** Branches we created (N×N converters, two capability tables) are
   removed. Branches the world forces on us (a vendor's 400, an old client's
   shape) cannot be removed, so each lives in one layer with a name, a source
   link and a regression test. Ask: is this branch the shape of the world or
   the shape of our code?
4. **Few, stable extension points.** A new protocol is one codec directory, a
   new semantic is one row in the feature table, a new vendor is one transport
   dialect, a new model is configuration. Ask: how many files does adding a new
   X touch? More than one means the extension point is in the wrong place.
5. **A rule has teeth or it is a wish.** A rule enforced by neither a test nor
   a review checklist item is not a rule. Ask: which test goes red when this is
   violated? If none, write the test before the rule.
6. **Deletion is part of delivery.** When a new path lands, the old one is
   deleted in the same PR or before the next release. Two generations of the
   same mechanism do not coexist. Ask: what did this PR delete?
7. **Every step leaves the system consistent.** Large work is split into PRs,
   but no PR leaves a half-migrated path, a flag that is never flipped, or a
   fallback that is never exercised. Ask: after this merges, which path is
   half-built?
8. **Types carry meaning; dynamic structures do not carry core logic.** No
   `map[string]any` for core semantics in Go, no new `any` in TypeScript,
   and "not provided" is distinguishable from "provided as zero" (`Option[T]`,
   the `store` tri-state). Ask: can this field tell absent from zero?

## Layout

```txt
apps/api                Go HTTP server entrypoint
apps/web                Vite + React + TypeScript console and marketing surface
apps/docs               Vocs documentation site
apps/worker             Background worker
internal/               Go controllers, services, models, relay adapters, middleware
packages/contracts      OpenAPI YAML and shared contract types
config/backend          Backend config templates and seed data
ops/scripts             Local development and deployment helper scripts
.local-results/         Local-only generated artifacts, ignored by git
```

## Local Artifacts

Do not scatter generated local artifacts such as `.redesign-review/`, `screenshots/`, or `build-out/` across the repository. Put local-only outputs under `.local-results/`:

| Directory                                       | Purpose                                       |
| ----------------------------------------------- | --------------------------------------------- |
| `.local-results/screenshots/warm-paper-latest/` | Playwright visual smoke-test output           |
| `.local-results/screenshots/snapshots/<name>/`  | Named screenshot snapshots                    |
| `.local-results/test-results/`                  | Playwright traces                             |
| `.local-results/playwright-report/`             | Playwright HTML report                        |
| `.local-results/docs-build/`                    | Local documentation build/sanity-check copies |

Default behavior is overwrite-only. Keep historical snapshots only when the task explicitly needs a before/after archive.

## Engineering Rules

These rules come from [CONTRIBUTING.md](./CONTRIBUTING.md) and should be treated as repo guardrails.

### Canonical Design Authority

- `brand/` is the repository's only design and product-UI reference.
- Only `brand/BRAND.md`, `brand/foundations/*.md`, and `brand/tokens/*` define production constraints.
- Design explorations record decision history. They are kept outside this repository and cited as `archive:brand/explorations/<dir>`. Do not implement an exploration unless a locked foundation explicitly names the selected direction.
- `apps/web/src/components/ui/` is the production React implementation of the Brand component language; pages compose it instead of creating private component systems.
- Production aliases may be generated from Brand tokens during migration, but product code must not redefine canonical color, typography, radius, shadow, or motion values.

### Avoid Touching Unless The Task Requires It

- Existing i18n keys: do not rename or re-key them. A change that deletes the
  last `t('...')` read of a key deletes that key from all four locales in the
  same PR.
- `packages/contracts/openapi/*.yaml`
- Go backend business logic
- Production deployment files for lazu.ai (deploy script, production Compose
  file and production config), which are not kept in this repository
- Existing MDX content beyond the requested docs change

### Do Not Add

- `import ... from '@douyinfe/semi-*'` or `antd`
- new `any` types; use `unknown` or concrete types
- `// @ts-ignore`; if unavoidable, use `// @ts-expect-error` with a reason
- new `.jsx` or `.js` business files; use `.tsx` or `.ts`

### Loading And Waiting

Derives from Principles 2 and 7.

Five rules, learned from a sign-in door that drew a password form before the
server had said which credentials exist. `tests/smoke/loading-discipline.spec.ts`
enforces the first two by machine; the rest are on review.

1. **Do not state what you do not know.** An unanswered query has no answer:
   `data ?? 0` is not zero, an empty array is not "nothing here", and `null` is
   not "the feature is off". Carry the query's own `isPending` and render
   `PendingValue` / `PendingBlock` (`components/ui/pending.tsx`) in place of any
   figure, count, total or empty-state sentence that reads off absent data.
2. **A placeholder must occupy the space its answer will take.** Same column,
   same width, same height, same position — verify by measuring both boxes, not
   by eye. A table reserves a page of rows; a chart reserves its height; a
   document reserves a viewport.
3. **Do not paint a wait that most people never have.** Hold the space, draw
   nothing for the first 300ms (`hooks/common/useDelayedReveal.ts`), and let one
   clock cover a route's whole wait rather than restarting it per component.
4. **Start the waits together, not one after the other.** A route's chunk and
   its data both begin on intent — `lib/route-prefetch.ts` warms a destination
   when its link is hovered or focused. Never make a fetch wait for a module
   that could have loaded beside it.
5. **Only the unknown part waits.** Frames, headers, grounds and controls that
   do not depend on the answer paint immediately.

### Required Practices

- Put new shared frontend components under `apps/web/src/components/ui/`.
- Read `brand/BRAND.md` and the relevant locked foundation before changing product UI, public web, Docs presentation, or product information architecture.
- Use Conventional Commits, for example `feat(web): ...` or `chore(repo): ...`.
- Validate frontend changes in proportion to risk. For routine small features and
  low-risk UI/navigation adjustments, finish the implementation and use focused
  checks such as `bun run typecheck`, targeted lint, or a direct browser check;
  do not run the full smoke suite by default. Run relevant smoke or end-to-end
  tests when changes affect authentication, authorization, billing, destructive
  operations, shared route contracts, or another high-risk workflow.
- For docs changes, run `npm run typecheck && npm run lint && npm run build` when feasible.

### Request Path

Derives from Principles 1, 5 and 6.

The request path runs from relay ingress until its response ends, including
relay middleware, controllers and adapters.

1. Count database and Redis round trips with `ops/bench/gateway`; include the
   measured change in the PR description. This benchmark stays outside CI.
2. New per-request facts use the batched relay event consumer. Do not add
   synchronous SQL writes to a warmed request path.
3. Schema validation and catalogue queries belong at startup or in explicit
   migrations. Do not add runtime database schema probes.
4. Lock only what a phase changes. Do not lock the same row twice in a phase,
   or a shared project/platform row for a read-only admission check.
5. Parse the body once. Reuse the parsed request and retained raw fields.
6. Capture and token shadow work use bounded background queues. A financial
   settlement completes only after a durable Redis event or fsynced spool.
7. Authentication, admission and settlement use `internal/requestpath`;
   update its query inventory when adding a cold or uncommon mechanism.
   Administrative queries use the registered Core or Telemetry store.
8. Ship an optimization enabled, or remove it; do not retain unused default-off
   faster paths.

### Read Path

Derives from Principles 1, 2 and 5.

Learned from 2026-10-10: the health worker decoded every routing JSON copy on
every run, crossed its five-second budget as rows accumulated, and stopped
probes and the model plaza's health for four days without anyone being told.

1. A fact that is filtered, joined, aggregated or sorted on is a typed column
   with an index. JSON copies (`routing_outcome_json`, `routing_decision_json`,
   `other`) are records for diagnostics and rebuilds, never a query surface.
   `TestNoReaderDecodesRoutingJSON` fails on new `::jsonb` reads of the routing
   copies; routing facts are the `ro_*` / `rd_*` columns on `logs`.
2. A query over history must be bounded by an indexed time column inside every
   branch of its union, not by an outer filter the planner may not push down.
3. A background job's failure must not freeze another job's output: report it,
   then let independent work run.
4. Anything that can silently degrade has an alert. Worker task failure
   streaks, a lagging route health rollup, slow SQL (`SLOW_SQL_ALERT_MS`,
   1s by default) and an upstream that keeps refusing a channel's billing or
   key mail `[Lazu ops]`; extend these rather than adding log-only checks.
5. A page that summarises history reads pre-aggregated hours, and still works
   when the job that maintains them is down. Route health is the model:
   `route_health_hourly` is recomputed per hour from raw facts, and readers
   compute the hours it has not reached themselves.
6. A list over history pages in the order of an index that also serves its
   filter, so LIMIT stops after one page. Log lists use `orderLogsNewestFirst`
   (`created_at`, then `id`); the project, user, token and channel filters
   each have a `(filter, created_at)` index (schema 37). The other admin
   filters (username, model, token name, group) have none yet and walk
   `created_at` backwards, so a sparse match over a wide window still reads
   past rows; a new filter gets its index with it.
   `TestLogListsOrderNewestFirst` fails on a log list ordered by id, and
   `TestPostgresLogPagesReadOnlyTheirPage` measures the indexed lists' plans.

### Protocol Layer

Derives from Principles 1, 2, 3, 4, 6 and 8. The design baseline is
[docs/protocol-conversion-architecture.md](./docs/protocol-conversion-architecture.md):
three invariants (the parsed request is immutable and every attempt is planned
from it; capability is decoupled from model names; refuse rather than convert
lossily). The rules below are what those invariants mean in a diff. Each names
its enforcement. A test marked *planned* does not exist yet; the protocol
roadmap adds it, and until then the rule is a review rule.

1. **A new protocol is one codec directory under `internal/protocol/textgen/codec/`
   and changes no handler.** A codec is five things: wire→IR, IR→wire,
   SSE→event IR, event IR→SSE, whole-response render. Handlers dispatch on the
   plan, never on a protocol family. A bridged attempt's body is
   `relay.BridgeBody`, keyed on the plan's upstream protocol; its reply is
   `channel.RunHubStream` with the upstream's decoder and the client's
   writer (`channel.HubClientFor`). A hub adds only a decoder and the side
   effects its upstream's events carry. The channel probe is planned and
   bridged the same way (`planProbeAttempt`, `BridgeBody`). Enforced by review; planned
   test: no protocol-family branch in `internal/relay/*_handler.go`.
2. **Every conversion is exactly one semantic hop:** the client codec decodes,
   the upstream codec encodes. No path goes through a third protocol's DTO.
   Planned test: `PlanAttempt` over all client×upstream pairs yields no
   intermediate protocol.
3. **No new model-name judgement in `internal/relay/channel`.** What a model
   can do comes from the capability snapshot, seeded from the model
   catalogue, never from `HasPrefix(model, "claude-…")` and its relatives.
   `TestNoNewModelNameJudgementsInChannels` (`internal/relay/channel/
   model_name_rules_test.go`) scans the channel packages; the judgements
   that existed when it was written are listed there as debt, per file, and
   the list may only shrink.
4. **The event IR carries content, not a protocol's lifecycle.** The events
   in `internal/protocol/textgen/event.go` are text, refusal, reasoning (with
   block boundaries, signatures and sealed items bound to their dialect),
   tool start, argument fragments, stop, usage and error. Items, content
   parts, blocks, sequence numbers, ids and indexes belong to each protocol's
   emitter; the shared usage fact (`dto.Usage`) and the parsed wire request
   kept beside the IR are the only `internal/dto` types the package refers
   to. `TestEventIRCarriesNoWireShapes` fails on a `dto.Responses*`,
   `dto.Claude*` or `dto.Gemini*` reference in the IR files.
5. **Refuse with a feature ID and a field path; degrade only from the
   registry.** A request that cannot be preserved returns 400 before the
   upstream call, naming `feature` and `path`. The only permitted lossy steps
   are rows of the degradation registry (`textgen.Degradations`,
   `internal/protocol/textgen/degrade.go`, [docs/protocol-degradation.md](./docs/protocol-degradation.md)):
   each stands in for one feature the channel is *known* not to carry
   (denied by the operator, or gapped on every endpoint), has an ID, a
   reason, a source link, and sets `X-Lazu-Warning` with `degraded:<id>`.
   Unknown is not unsupported. Nothing is learned at runtime from a vendor's
   400; this is a multi-tenant gateway, and a memo keyed on provider+model
   that any tenant's request can poison is not a compatibility mechanism.
   `TestDegradationRegistryIsComplete` fails on a rule without a test case.
6. **Vendor quirks live only in the wire dialect patch layer,** applied after
   the outbound body is compiled: one named function per quirk, one regression
   test, one source link (issue, vendor doc, request ID). Codecs do not contain
   vendor conditionals. Enforced by review; the PR template asks for the quirk
   ID.
7. **Golden fixtures before deletion; two generations for at most one
   release.** Before an old converter is removed, its behaviour is recorded as
   fixtures the new codec must pass (the Chat→Messages converter's are
   `codec/anthropic_messages/testdata/golden/`, held by
   `TestEncodeMatchesLegacyGoldens`). Legacy pairwise converters are deleted
   in the PR that lands their codec or the release after it, and gain no new
   features meanwhile: `relay-claude.go`'s Chat↔Messages pair went with the
   Messages hub (2026-10-21). The Claude-client request converter and its
   switch went in the following release; its output on the cases it got
   right is `service/testdata/claude_bridge/`, held by
   `TestClaudeToChatMatchesTheGoldenFiles`. Gemini channels serve Chat,
   Responses and Claude clients through the Gemini codec (2026-10-26); the
   Chat→Gemini converter, the legacy Gemini Chat handlers and the Chat→Claude
   response converters went with that slice, and the converter's output is
   `channel/gemini/testdata/chat_to_gemini/`, held by
   `TestBridgedChatToGeminiMatchesTheGoldenFiles`. Gemini clients are parsed
   into the IR and planned like the others since the following slice; the
   Gemini-client converters in `service/convert.go` went with it, and their
   output is `service/testdata/gemini_to_chat/` and
   `service/testdata/chat_to_gemini_reply/`, held by
   `TestGeminiToChatMatchesTheGoldenFiles` and
   `TestChatReplyToGeminiMatchesTheGoldenFiles`. Enforced by review.
8. **Stream tests use recorded upstream SSE, not hand-written ideal JSON.**
   Recordings live in `internal/protocol/textgen/testdata/upstream/<vendor>/`
   (made by `go test -tags record` in `internal/relay/contracttest`, keys from
   the environment only) and load through
   `internal/protocol/textgen/fixtures`. A stream decoder has at least one
   test that feeds its vendor's recording through it. Enforced by review.
9. **Stateless by default.** `previous_response_id`, `conversation` and
   explicit `store: true` return a stable 400 on bridged paths until a separate
   ADR designs server-side state. Enforced by the planner's tests.
10. **Decision changes go to the ADR before the code.** A PR under
    `internal/protocol` that changes a decision in the architecture document
    updates that document in the same PR; one that does not says "no decision
    change" in its description. Enforced by review.

## Lessons

What recent work got wrong, and the rule that would have caught it. Keep this
to the last few; a lesson that has become a rule with a test can be removed.

- **2026-10-23, project log page.** A five-row page of a small project read
  40 000 other rows. The list was ordered by id, a project's rows were found
  through an OR fallback to its owner's project-less rows that no index served
  in time order, and a planner fence covered only windows of up to a day.
  Rule that catches it: Read Path 6 (Principle 5); schema 37 gave the
  fallback's rows their project and removed the fence (Principle 6).

- **2026-10-20, Claude thinking mapping.** Haiku 5.5 with `reasoning_effort`
  was encoded as legacy `thinking.enabled` with a token budget and refused by
  the upstream, because `SupportsAdaptiveThinkingModel` recognised only two
  Opus prefixes. The architecture document had forbidden model-name
  judgements a month earlier, but only for the code it touched. Rule that
  catches it: Protocol Layer 3 (Principle 1, 5), now enforced by
  `TestNoNewModelNameJudgementsInChannels`; the Claude policy lives on the
  catalog row (`reasoning_options.mode` / `.off`, `internal/model/reasoning_policy.go`).
- **2026-10-20, two generations of converters.** The IR codec shipped for
  Chat↔Responses only; the Claude and Gemini pairwise converters stayed, got
  no maintenance, and broke on the next model. Rule that catches it: Protocol
  Layer 7 (Principle 6).
- **2026-10-20, one-PR delivery.** "One complete project, one PR" was the
  reason Claude and Gemini codecs were cut from the first delivery. Large work
  is split so each PR leaves the system consistent (Principle 7); it is not
  cut so one PR can finish.

## Code Review Rules

Report only P0 and P1 findings. Leave out everything below P1.

- P0 — blocks merge: loses or mis-charges money, loses or corrupts data,
  exposes a secret or credential, breaks authentication or authorization,
  takes production down, or makes a deploy unrecoverable.
- P1 — fix before merge: a real bug with a concrete input or state that
  triggers it, a regression in existing behaviour, or a migration that is
  unsafe on existing data.

Do not report formatting, naming, comment wording, style, refactoring ideas,
missing tests on their own, or anything lint, typecheck or CI already checks.
If you are unsure whether something reaches P1, leave it out. Every finding
names the input or state that triggers it.

Treat as at least P1 in this repository:

- Billing and settlement: double charging, charging without a durable Redis
  event or fsynced spool, lost settlement, races on quota or balance.
- Request path: a new synchronous SQL write or runtime schema probe on the
  warmed path, or the same row locked twice in one phase.
- Read path: filtering, joining or sorting on the routing JSON copies, or a
  history query not bounded by an indexed time column in every union branch.
- A UI figure for money or usage that shows 0 or "empty" while its query is
  still pending.
- A real secret, production credential or private endpoint in a tracked file
  (this repository is public).
- A new `*Context`, batch or codec path whose predecessor is still compiled
  and reached only by tests (Principle 6): the PR moves those tests onto the
  new path and deletes the old one, or names the release that will.

## Common Commands

```bash
# Web console
cd apps/web
bun install
bun run dev
bun run build
bun run typecheck
bun run eslint
bun run test:smoke

# Docs site
cd apps/docs
npm run dev -- --port 3002
npm run build

# Backend
./run.sh
./rebuild.sh

# Docker stack
./ops/scripts/stack.sh up --build
```

## Deployment Notes

Self-hosted deployments should provide `DATABASE_URL`, `ROOT_USERNAME`, and `ROOT_PASSWORD` at first startup. The backend migrates the schema and syncs the root account on startup; there is no browser-based install wizard.

Credentials and startup configuration belong in environment variables or backend config files. Changing them requires restarting the backend container. Operational values such as pricing, announcements, model ratios, and channel toggles belong in the console UI.

## Agent Execution Notes

- Read [CONTRIBUTING.md](./CONTRIBUTING.md) before large changes.
- If blocked for more than about 30 minutes, leave a short `TODO(<context>)` comment, skip that item, and report the blocker.
- Keep changes scoped to the request. Opportunistic rewrites are a maintenance risk.
- A PR that adds a twin of an existing function (a `*Context` variant, a batch
  version, a codec replacing a converter) lists in its description every
  function it retires and moves their tests onto the new code in the same PR.
  Keeping the old one as a wrapper so tests stay green is how dead code
  accumulates.
- Store local generated files under `.local-results/<purpose>/`.
- Open new GitHub pull requests as ready for review by default. Do not create draft PRs unless the user explicitly asks for draft mode.
- Work on a branch in its own worktree, created from `origin/main` after a fetch; the `main` checkout is for reading and pulling. When your pull request merges, fast-forward `main`, remove the worktree and force-delete the branch (pull requests are squash-merged, so `git branch -d` refuses). Pull before smoke-testing a release.
- Do not commit real secrets, production credentials, private endpoints, or local databases.
