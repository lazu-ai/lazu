# Lazu

**Where agents get their models.** One key for the text, image and video models your agents call: Claude, GPT, Gemini and DeepSeek for code, GPT Image for images, Seedance for video.

> **👤 Looking to use Lazu?** → **[lazu.ai/docs](https://lazu.ai/docs)** for the user-facing API documentation (Quickstart, Pricing, Endpoints, Examples).
>
> **🛠 Working on Lazu itself?** → keep reading, you're in the right place.

---

Lazu is an AGPL-3.0-or-later OpenAI-compatible AI gateway, operations console, and developer documentation site. It provides approved model access, automatic failover, API Key policy, usage and request receipts, quota and billing controls, public Route health, and protected upstream operations.

The current product boundary is the reliable gateway core. Intelligent-routing experiments may remain in the codebase, but they are deferred from default navigation and the primary product story. The canonical visual and product design reference is [`brand/`](./brand/README.md).

- **API endpoint**: `https://api.lazu.ai`
- **Public docs (users)**: [`https://lazu.ai/docs`](https://lazu.ai/docs)
- **Maintainer notes (this repo)**: [`docs/`](./docs)

Self-hosted deployments should configure their own API, web, docs, database, storage, and email settings through environment variables and backend config templates.

## Workspace

| Path              | Purpose                                                                                                                                                           |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `apps/api/`       | API server entrypoint                                                                                                                                             |
| `apps/web/`       | Web console                                                                                                                                                       |
| `apps/docs/`      | Active Vocs documentation source (en/zh/zh-TW/ja)                                                                                                                 |
| `apps/worker/`    | Background jobs                                                                                                                                                   |
| `internal/`       | Backend controllers, services, models, relay adapters, middleware, settings                                                                                       |
| `packages/`       | Shared contracts and pure domain rules                                                                                                                            |
| `config/`         | Backend config, env templates, seed data                                                                                                                          |
| `ops/`            | Operational scripts and deployment helpers                                                                                                                        |
| `docs/`           | Maintainer notes, deployment guidance, architecture notes, and glossary material                                                                                  |
| `.local-results/` | （仅本地，已 gitignore）Playwright 截图 / trace / 报告、本地构建副本等"跑出来的产物"统一存放，约定见 [CONTRIBUTING.md](./CONTRIBUTING.md#本地产物local-artifacts) |

Shared package sources:

- `packages/contracts/openapi/lazu-api-reference.v1.yaml`: developer API reference used by the docs site

Error codes, envelopes and the rules for adding errors are documented in [docs/ERRORS.md](./docs/ERRORS.md).

## Quick start (Docker)

```bash
cp .env.example .env        # set ROOT_PASSWORD and the database passwords
docker compose --env-file .env -f platform/docker/docker-compose.yml up --build -d
```

Open <http://localhost:3000> and sign in with `ROOT_USERNAME` / `ROOT_PASSWORD`
(`root` / `change-me-local-root` if you left them empty).
Then add an upstream under 模型运营 / Model operations, create an API key, and
call `http://localhost:3001/v1/chat/completions` with it.

The stack runs the gateway (port `3001`), its background jobs, the web console
(port `3000`), separate Core and Telemetry PostgreSQL stores and Redis. On first
start the gateway creates its schema and opens its financial ledger; later
starts leave both alone. There is no browser-based install step.

Everything beyond the gateway is optional and stays off until configured:

| Feature | Configure |
| --- | --- |
| Mail (sign-up codes, receipts, alerts) | `RESEND_API_KEY` and `ADMIN_EMAIL`, or `smtp` in `config/backend/config.yaml` |
| Card top-ups | `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_ID` |
| Files API and request captures | `R2_ENDPOINT`, `R2_BUCKET`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`; without them the Files API answers `503 storage_not_configured` |
| Links in mail and payments | `FRONTEND_BASE_URL`, and `site` in `config/backend/config.yaml` |
| Probes against client-restricted upstreams | `probe_identities` in `config/backend/config.yaml` |

Integration variables reach the containers through
`platform/docker/docker-compose.integrations.example.yml`; copy it to
`docker-compose.integrations.yml` and pass it as a second `-f`.

The bundled public pages (home, models, blog, legal) are lazu.ai's. Replace the
legal documents under 设置 / Settings before you open your deployment to others.

For the full deployment checklist, see [docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md).

## Local Development

```bash
./run.sh
```

`./run.sh` is the fast path: it starts the existing local services without
clearing caches or reinstalling dependencies. When dependencies or build
caches need a clean reset, use:

```bash
./rebuild.sh
```

The local development stack reads `.env`; keep production values in your
deployment's own secret store. The local stack uses PostgreSQL on
`127.0.0.1:10000`, Redis on `127.0.0.1:10001`, the API on `3000`, the web app
on `5173`, and docs on `3002`.

Other repository commands:

```bash
make help
```

Local development intentionally has only two user-facing entrypoints:
`./run.sh` for a fast start and `./rebuild.sh` for a clean dependency/cache
rebuild followed by startup. The Docker stack and scripts under `ops/scripts/`
are deployment or maintenance internals, not additional local startup paths.

## Docker Stack

```bash
./ops/scripts/stack.sh up --build
```

## Docs

Public docs source lives in `apps/docs/` (Vocs).

Start with [the maintainer index](docs/README.md) for current contracts and
operations.

Current docs layout:

- `apps/docs/vocs.config.ts`: Vocs site configuration, sidebar, search, theme
- `apps/docs/pages/index.mdx`: docs home (English default)
- `apps/docs/pages/examples.mdx`: public code examples
- `apps/docs/pages/changelog.mdx`: public documentation changelog
- `apps/docs/pages/api-reference.mdx`: Scalar-rendered OpenAPI reference
- `apps/docs/pages/{zh,zh-TW,ja}/*`: per-locale variants
- `docs/i18n.md`: translation glossary

API reference source lives in `packages/contracts/openapi/lazu-api-reference.v1.yaml`.

Docs preview:

```bash
cd apps/docs
npm install
npm run dev -- --port 3002
```

Docs build:

```bash
cd apps/docs
npm run build
```

Canonical docs path: `https://lazu.ai/docs` (the web app proxies `/docs` to the docs container; `docs.lazu.ai` 301s there).

Project docs should stay flat and focused. Prefer code, tests, OpenAPI, and generated docs over new Markdown.

## Config

Backend config resolution order:

1. `APP_CONFIG_FILE`
2. `config/backend/config.yaml`
3. `config/backend/config.yml`
4. `/app/config/backend/config.yaml`
5. `/app/config/backend/config.yml`

Credentials and startup settings belong in env or `config/backend/config.yaml`; changing them requires a backend container restart. Daily operational values such as pricing, notices, ratios, and toggles are changed in the console UI.

Features a deployment compiles in on top of this repository attach through the hooks in [docs/EXTENSIONS.md](./docs/EXTENSIONS.md).

## Security

- Do not commit real config secrets.
- Keep `config/backend/config.yaml` local or environment-provided.
- Rotate any credential that was ever committed or pasted into logs.
- Production deployments must use strong `SESSION_SECRET` and `CRYPTO_SECRET` values.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for the workflow and the sign-off
rule, [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md) for community standards, and
[SECURITY.md](./SECURITY.md) to report a vulnerability privately.

## License

Lazu is distributed under the GNU Affero General Public License v3.0 or later;
see [LICENSE](./LICENSE). If you run a modified version as a network service,
the license requires you to offer its source to the people who use it: set
`site.source_url` in `config/backend/config.yaml` and the console links to it.

Lazu is based on [new-api](https://github.com/QuantumNous/new-api)
(AGPL-3.0), which is based on [one-api](https://github.com/songquanpeng/one-api)
(MIT). Their copyright and attribution are kept in [NOTICE](./NOTICE) and the
applicable source-file headers, and the console carries the attribution the
upstream's additional license terms require. Preserve all of these when
distributing modified copies. Third-party licenses are listed in
[THIRD_PARTY.md](./THIRD_PARTY.md).
