# Lazu maintainer documentation

This directory contains durable maintainer documentation: contracts,
architecture and self-hosted operation. It is not the source of truth for the
public docs site or the product design system.

## Boundaries

- `brand/` — locked brand, design-system, and product-UI principles.
- `apps/docs/` — public documentation and the rendered developer API reference.
- `packages/contracts/openapi/` — canonical API reference sources. Rendered
  API references are generated from these files; public tutorials are authored
  separately in `apps/docs/pages/`.
- `docs/` — durable architecture, contract and operations notes.
- Plans, dated audits, decision records and design explorations
  for the hosted lazu.ai service are kept outside this repository.
  References to them use the `archive:<path>` form.
- `.local-results/work/` — temporary AI-assisted plans, experiments, and task
  notes. This path is local-only and should normally be deleted when the task
  is complete.

## Lifecycle

Put a document in `docs/` only when it has a durable audience or operational
value. A task-specific plan that is expected to disappear after implementation
belongs in `.local-results/work/<task>/`; if it needs review in a PR, use the
PR description or an explicitly temporary `docs/work/` file and remove it
before merge.

Public API or product documentation must be authored in `apps/docs/` and
contract changes must start in `packages/contracts/openapi/`. Do not copy
brand principles, private deployment notes, or AI scratch content into the
public docs site.

Current documents describe stable behavior and link to implementation or tests.
Keep dated observations in audits, mark proposals as planned, and link a
superseded record to its replacement. Completed task checklists and unselected
prototypes are removed after their durable conclusions are captured; Git keeps
their history. Retain selected Brand references and their required assets.

## Find the right document

| Task | Read |
| --- | --- |
| Set up or contribute | [Project README](../README.md), [engineering rules](../CONTRIBUTING.md), [Agent entry](../AGENTS.md) |
| Understand protocol behavior | [Protocol conversion](protocol-conversion-architecture.md), [errors](ERRORS.md), [request lifecycle](request-lifecycle-contract.md) |
| Understand charges and account tiers | [Billing contract](billing-contract.md), [tier implementation — not in use](tier-policy.md) |
| Deploy and operate | [Deployment](DEPLOYMENT.md), [Files API operations](../ops/scripts/files-api/README.md) |
| Change public docs or translations | [Docs authoring and generation](../apps/docs/README.md), [translation glossary](i18n.md) |
| Change product UI | [Brand index](../brand/README.md); current rules start at [BRAND.md](../brand/BRAND.md) |
| Report security issues | [Security policy](../SECURITY.md) |

Keep runtime legal defaults in `internal/setting/system_setting/legal_defaults/`:
Go embeds those files and the web SEO generator reads them. They are application
content, not disposable documentation. Keep LICENSE, NOTICE, source copyright headers and asset licenses.

## Current implementation and priorities

Lazu is the Go gateway in `apps/api` / `internal`, the Go background worker in
`apps/worker`, the React Console in `apps/web`, and the Vocs public site in
`apps/docs`. Runtime and startup authority is the current code and root README.
There is no Rust data plane or Cerul control-plane service in this repository.
The abandoned three-runtime product proposal was removed; Git retains it for
historical investigation, not as an implementation roadmap.

Prioritize correct protocol round trips, explainable charges and refunds, tenant
isolation, then operator diagnostics and maintainability. Additions to model or
product scope require separate decisions. Current contracts:

- [Protocol conversion](protocol-conversion-architecture.md): native/bridge boundaries and attempt isolation.
- [Errors](ERRORS.md): stable codes, HTTP status and retry classification.
- [Billing](billing-contract.md): reservations, usage provenance and recovery.
- [Request lifecycle](request-lifecycle-contract.md): persisted facts and compatible reads.

Brand explorations do not supersede current implementation or locked
foundations.

### Legacy vocabulary and compatibility

| Existing name | Current meaning | Change boundary |
| --- | --- | --- |
| `quota` | Integer microUSD account balance or key spend allowance, according to the owner | Do not change units or historical ledger amounts through a rename. |
| User `group` | Account tier / pricing adjustment scope | Distinct from channel selection; keep persisted/API values compatible. |
| Channel `group` / lane | Routing and price lane: the stable lane (slug `direct`, projected as `stable`) and the agent lane (slug `cheap`, projected as `eco`) | Slugs are never renamed; keep the customer-facing projection separate from provider identity. |
| `channel` | A configured upstream endpoint, credentials and transport settings | Required gateway concept, not obsolete branding. |
| Legacy API key representations | `sk-lazu-` / `sk-` / legacy bare-key lookup candidates | New keys use `sk-lazu-`; retain old reads until all dependents migrate. |

Names with active API/database consumers are compatibility contracts. Remove
unused internal symbols and obsolete presentation independently. Keep LICENSE, NOTICE, source copyright headers and asset licenses when deleting obsolete product wording.

| Compatibility surface | Writer / reader | Exit condition and rollback |
| --- | --- | --- |
| API keys | `GenerateKey` writes `sk-lazu-`; `APIKeyLookupCandidates` and digest migration accept legacy forms | Remove an alias only with deployed-client evidence and an announced migration. Restoring the reader must not rotate existing keys. |
| `SQL_DSN` | Startup reads it only after `DATABASE_URL` and YAML database URL; templates use the current database configuration | Keep fallback until self-hosted configuration migration is verified. Restoring fallback does not change database contents. |
| `LOG_SQL_DSN` | Active independent log-database configuration | This is an active feature, not an obsolete alias; retain it. |
| `quota`, tier `group`, lane `group`, channel-type enum | ORM and billing/API clients read persisted values | Rename internal helpers independently. External changes need explicit versioning, migration and reversible dual reads; never reinterpret historical money. |
| Old Console URLs | Browser redirects `/console/routes` and `/console/model-market` to `/console/models` with URL state | Retain until usage evidence supports removal; rollback can restore routing without data migration. |
| Historical usage / errors | Receipts read old `Other`/outcome data; existing Console consumers read `success` envelopes | New optional measurement fields do not rewrite old records. Migrate the endpoint and all known consumers together. |

Workspace Manager and Key Pass are Console scopes, not additional numeric
backend roles. The current backend enforces account/key ownership and the
member/admin/root boundary. A Console scope switch must not be presented as
proof of a new multi-workspace tenancy model.
