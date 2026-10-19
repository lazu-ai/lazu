# AGENTS.md

Repo-level context for AI agents (Codex, Claude Code, Cursor, and similar tools) working in this codebase. Read this first, then inspect the relevant code before editing.

## What This Is

Lazu is an open-source OpenAI-compatible AI gateway, operations console, and public documentation site. The product, package namespace, database and service names are all Lazu. Earlier product names are retired from product branding, code identifiers and file paths. Retain legal attribution in NOTICE and the original source copyright headers; the scanner permits only these explicit legal exceptions. CI runs `ops/scripts/check-brand-residue.sh` to enforce this.

See [README.md](./README.md) for the full project structure and setup instructions.

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
- Design explorations record decision history. They live in the private ops repository and are cited as `lazu-ops:brand/explorations/<dir>`; this repository does not contain them. Do not implement an exploration unless a locked foundation explicitly names the selected direction.
- `apps/web/src/components/ui/` is the production React implementation of the Brand component language; pages compose it instead of creating private component systems.
- Production aliases may be generated from Brand tokens during migration, but product code must not redefine canonical color, typography, radius, shadow, or motion values.

### Avoid Touching Unless The Task Requires It

- `t('...')` i18n key calls
- `packages/contracts/openapi/*.yaml`
- Go backend business logic
- Production deployment files for lazu.ai (deploy script, production Compose
  file and production config), which move to the private ops repository
- Existing MDX content beyond the requested docs change

### Do Not Add

- `import ... from '@douyinfe/semi-*'` or `antd`
- new `any` types; use `unknown` or concrete types
- `// @ts-ignore`; if unavoidable, use `// @ts-expect-error` with a reason
- new `.jsx` or `.js` business files; use `.tsx` or `.ts`

### Loading And Waiting

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
   streaks, a lagging route health rollup and slow SQL (`SLOW_SQL_ALERT_MS`,
   1s by default) mail `[Lazu ops]`; extend these rather than adding log-only
   checks.
5. A page that summarises history reads pre-aggregated hours, and still works
   when the job that maintains them is down. Route health is the model:
   `route_health_hourly` is recomputed per hour from raw facts, and readers
   compute the hours it has not reached themselves.

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
- Store local generated files under `.local-results/<purpose>/`.
- Open new GitHub pull requests as ready for review by default. Do not create draft PRs unless the user explicitly asks for draft mode.
- Do not commit real secrets, production credentials, private endpoints, or local databases.
