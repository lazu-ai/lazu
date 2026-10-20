# Lazu Deployment

This page covers running Lazu with Docker Compose: the bundled stack in
`platform/docker/docker-compose.yml`, and what a production deployment adds.

## Prerequisites

- PostgreSQL 14+
- Redis 6+
- Docker and Docker Compose

## Prebuilt images

The bundled stack builds from source. To run published images instead, use
`ghcr.io/lazu-ai/lazu/api`, `ghcr.io/lazu-ai/lazu/frontend` and
`ghcr.io/lazu-ai/lazu/docs` (linux/amd64, public):

| Tag | Points at |
| --- | --- |
| `vX.Y.Z` | A release. The API reports this version in `/api/status`. |
| `latest` | The newest stable release. |
| `main` | The newest build of each image from the main branch. |
| `<commit sha>` | That main-branch commit; unchanged images are aliases of their last build. |

`api` also serves the background worker (`/worker`) and the schema tool
(`/migrate`). Pin a release tag in production and move to a new one
deliberately: a release can raise the database schema version.

## Production checklist

A deployment other people use needs these set, in `.env` or your secret
store, with the integrations you enable:

| Purpose | Required variables |
| --- | --- |
| Database and Redis | `DATABASE_URL`, `REDIS_PASSWORD`, `POSTGRES_PASSWORD` |
| Runtime secrets | `SESSION_SECRET`, `CRYPTO_SECRET` |
| Root account | `ROOT_USERNAME`, `ROOT_PASSWORD` |
| Administrative email | `ADMIN_EMAIL`, `RESEND_API_KEY` |
| Stripe | `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PRICE_ID` |
| Object storage | `R2_ENDPOINT`, `R2_BUCKET`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY` |

Keep runtime secrets strong and stable when restarting or moving the database.
Root credentials bootstrap a new root account and sync configured values at
startup. Review `config/backend/config.yaml` alongside them: `site`,
`email_notifications` and `probe_identities` describe your deployment.

Do not supply placeholder credentials for integrations you do not use; leave
them empty and the feature stays off.

## Optional integration settings

| Capability | Configuration |
| --- | --- |
| SMTP transport | `SMTP_ACCOUNT`, `SMTP_TOKEN` and host settings for the selected mail transport |
| Telegram notifications | `TELEGRAM_BOT_TOKEN` |
| Separate browser origins | `FRONTEND_BASE_URL`, and exact `CORS_ALLOW_ORIGINS` where needed; see the sign-in guidance below |
| Cloudflare edge purge on deploy | `CF_API_TOKEN` (a token scoped to Zone → Cache Purge for the site's zone) and `CF_ZONE_ID`; see below |

Starting containers does not verify payment, email or object storage workflows.
Validate each enabled integration with its actual provider configuration.

### Fetching client-supplied media URLs

When a request carries an image or file by URL and the selected provider
only accepts bytes, the gateway downloads the file itself. That download is
governed by the `fetch_setting.*` options (stored in the `option` table and
applied without a restart):

| Option | Default | Meaning |
| --- | --- | --- |
| `fetch_setting.enable_ssrf_protection` | `true` | Apply the policy below. Switching it off is a deliberate choice to dial anything. |
| `fetch_setting.allowed_ports` | `["80","443","8080","8443"]` | Ports the gateway fetches from. |
| `fetch_setting.allow_private_ip` | `false` | Allow every private address. Prefer `private_allow_list`. |
| `fetch_setting.private_allow_list` | `[]` | Private sources fetched on purpose: hostnames (`minio.internal`, `*.lan.example`), single IPs or CIDRs (`10.0.5.0/24`), as a JSON array. |
| `fetch_setting.domain_list`, `domain_filter_mode` | `[]`, deny-list | Optional host allow/deny list. |
| `fetch_setting.ip_list`, `ip_filter_mode`, `apply_ip_filter_for_domain` | `[]`, deny-list, `false` | Optional address allow/deny list. |

With the policy on, a URL is refused before any connection when its scheme
is not http or https, its port is not allowed, or its host is, or resolves
to, a loopback, RFC 1918, link-local (169.254/16 and the cloud metadata
service, fe80::/10), CGNAT (100.64/10), unique-local (fc00::/7), multicast,
unspecified or reserved address, including the IPv4-mapped IPv6 forms. The
connection is pinned to the address that was checked (the original host is
kept for the Host header and TLS), the dialer refuses any other address, and
every redirect is checked the same way, at most 5 hops. A refusal is a 400
`invalid_request` that names the policy to the client without disclosing
what an internal name resolved to; it is never retried on another channel
and never counts against the channel's health.

`private_allow_list` is the way to let the gateway read from an internal
object store or a LAN image host: list that host or its subnet and leave
`allow_private_ip` off. Ports and the domain lists still apply to listed
sources.

Two paths check less. With `HTTP_PROXY` / `HTTPS_PROXY` set, the proxy
resolves the name and dials; the gateway still refuses the URL and every
redirect hop by policy, but cannot pin the address the proxy connects to.
When a Cloudflare worker (`WorkerUrl`) does the fetching, the policy is
checked before the URL is handed over, and the worker follows redirects on
its own.

### Cloudflare edge caching of public pages

The public pages (home, FAQ, about, legal, model pages, blog, docs and their
`.md` siblings) are identical for every visitor, so Cloudflare can serve them
from the edge instead of the origin. The origin already says how long:
`CDN-Cache-Control: max-age=300` on prerendered and docs pages, `s-maxage` or
`max-age` on the API-rendered `/models` and `/blog`. Browsers still revalidate
every time. Console, sign-in, payment and API routes are never cached.

1. Purge the zone after every deploy or rollback (a token scoped to Zone →
   Cache Purge, `CF_API_TOKEN`, and the zone's `CF_ZONE_ID`), so an edge copy
   never outlives the bundles it points at.
2. In Cloudflare, add a Cache Rule. It matches these paths on the site host
   (`/`, `/faq`, `/about`, `/privacy`, `/terms`, `/llms.txt`, anything under
   `/zh/`, `/zh-TW/`, `/ja/`, `/models`, `/blog` or `/docs`, and `.md`
   files). Set it to **Eligible for cache**, with **Edge TTL** set to
   *Use cache-control header if present, bypass cache if not*.

Enable the rule only after step 1, or a deploy can leave old pages at the
edge for up to five minutes.

## Model metadata synchronization: upgrade configuration

The console's **upstream metadata preview and synchronization** require explicit
endpoint configuration. Before upgrading an existing installation, set both
`MODEL_METADATA_MODELS_URL` and `MODEL_METADATA_VENDORS_URL` in
`.env`. The built-in endpoint and `SYNC_UPSTREAM_BASE` setting are
no longer used. Existing installations must migrate their effective URLs;
merely retaining their former base-URL setting is insufficient.

To preserve the current provider and any custom base URL, open metadata sync
preview on the running, pre-upgrade console. In the browser Network panel,
read `data.source.models_url` and `data.source.vendors_url` from
`GET /api/models/sync_upstream/preview`. Copy those complete JSON endpoint URLs
into the two default variables. Repeat the preview with `?locale=en`; for each
localized URL, replace the locale path segment `en` with `{locale}` and save it
in the corresponding template variable below. Do not substitute the provider's
homepage or a base URL for a JSON endpoint. For a fresh installation, choose a
compatible dataset provider, such as the
[model metadata dataset](https://github.com/basellm/llm-metadata), and use its
published model and vendor JSON endpoints.

| Variable | Required for | Value |
| --- | --- | --- |
| `MODEL_METADATA_MODELS_URL` | Default preview and sync | Complete model JSON endpoint URL |
| `MODEL_METADATA_VENDORS_URL` | Default preview and sync | Complete vendor JSON endpoint URL |
| `MODEL_METADATA_I18N_MODELS_URL_TEMPLATE` | Localized preview and sync | Model URL template containing `{locale}` |
| `MODEL_METADATA_I18N_VENDORS_URL_TEMPLATE` | Localized preview and sync | Vendor URL template containing `{locale}` |

The URLs must use HTTP or HTTPS. Localized sync supports `en`, `zh` and `ja`;
configure both templates if operators use localized sync. A missing default
pair disables metadata sync, and a missing localized pair disables only
localized sync. Both cases return an explicit `success: false` configuration
message; they do not prevent the API or existing models from serving traffic.

The production Compose file passes these four variables to the backend.
Custom Compose or service-manager deployments must inject them explicitly as
well. Recreate the backend after editing its environment, then open default
and localized sync previews and confirm `success: true` and the expected
`data.source` URLs before considering the upgrade complete. Saving an env file
alone does not change an already-running container.

Use `APP_ENV=production` for the API and worker. For fail-closed compatibility,
any environment variable ending in `_ENV` with value `production` still enables
production secret requirements and secure-cookie defaults, including retired
or deployment-specific names. Migrate these markers to `APP_ENV`; do not rely
on removing a recognized name to disable production protections.

## Planned stablecoin payments

Stablecoin or other onchain payments are **not currently enabled** by this
deployment configuration. Do not add a wallet payment button or production
secrets until a payment provider has approved the merchant account.

## Google, GitHub, and enterprise sign-in

Password sign-in works without an external identity provider. Google, GitHub,
and one generic enterprise OIDC provider can be enabled independently and can
appear together on the login and registration pages. A provider is hidden
unless its required client id and secret are both configured.

For Google, create a Web application OAuth client and register this authorized
redirect URI on the **console origin**. The production console proxies `/api`
to the backend, so starting and completing OAuth on one browser origin keeps
the signed transaction and session cookies on the same host:

```text
https://<console-domain>/api/auth/oauth/google/callback
```

Then set:

```bash
GOOGLE_OAUTH_CLIENT_ID=<client-id>
GOOGLE_OAUTH_CLIENT_SECRET=<client-secret>
GOOGLE_OAUTH_REDIRECT_URL=https://<console-domain>/api/auth/oauth/google/callback
```

For GitHub, create an OAuth App and set its Authorization callback URL to:

```text
https://<console-domain>/api/auth/oauth/github/callback
```

Then set:

```bash
GITHUB_OAUTH_CLIENT_ID=<client-id>
GITHUB_OAUTH_CLIENT_SECRET=<client-secret>
GITHUB_OAUTH_REDIRECT_URL=https://<console-domain>/api/auth/oauth/github/callback
```

GitHub sign-in requests only `user:email`, uses the immutable numeric GitHub
user id as the external subject, and links an existing Lazu account only when
GitHub returns a verified email address. Google sign-in verifies the signed
OIDC ID token and nonce before using its subject and verified email.

Also set `FRONTEND_BASE_URL=https://<console-domain>` so browser-facing links
and completed callbacks return to the console. If a redirect URL is omitted,
Lazu derives it only for localhost or requests received through a trusted
proxy; production deployments should set the redirect URLs explicitly.

If a custom deployment intentionally sends browser API requests to a different
registrable site instead of using the console's `/api` proxy, build the web app
with `VITE_REACT_APP_SERVER_URL` set to that API origin and configure
`SESSION_COOKIE_SAME_SITE=none` together with `SESSION_COOKIE_SECURE=true`.
Set `CORS_ALLOW_ORIGINS=https://<console-domain>` so the API allows that exact
console origin with credentials. Do not use this mode merely for subdomains;
the console-origin callback above is simpler and keeps the default
`SameSite=Strict` protection.

The hosted production Compose file defaults this allowlist to
`https://lazu.ai`, which covers the console and the docs workbench (served at
`https://lazu.ai/docs`) calling `api.lazu.ai` directly. Self-hosted deployments and custom split-origin
deployments must override it with their own exact origins; do not use `*` with
credentialed requests. A same-origin console that proxies `/api` does not need
this variable for its normal browser calls.

Generic enterprise OIDC remains available through `OIDC_ENABLED=true`,
`OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and
`OIDC_REDIRECT_URL=https://<console-domain>/api/auth/oidc/callback`. Optional claim,
role-mapping, auto-create, and break-glass controls use the existing `OIDC_*`
variables exposed by the production Compose file.

## Config Schema

`config/backend/config.yaml` is the canonical schema reference. Production env
values should be injected through `.env` or deployment secrets, with
yaml placeholders such as `${DATABASE_URL}` kept in source-controlled templates.

Keep `CRYPTO_SECRET` stable for the lifetime of a database. Rotating or losing
it makes previously encrypted upstream credentials unreadable. If `/readyz`
fails after moving an environment, compare the database target, migration
state, `CRYPTO_SECRET` and `SESSION_SECRET` with the source deployment before
changing any stored credentials. `/api/status` proves that the process can
answer; only `/readyz` proves that migrations and required dependencies are
ready for traffic.

## Object Storage For Files API

`/v1/files` stores uploaded bytes in `artifact_storage`. The default config template maps Cloudflare R2 or any S3-compatible storage to:

```bash
R2_ENDPOINT=https://<account_id>.r2.cloudflarestorage.com
R2_BUCKET=lazu-user-files
R2_ACCESS_KEY_ID=<r2-access-key-id>
R2_SECRET_ACCESS_KEY=<r2-secret-access-key>
```

Create a private bucket in R2, create an R2 API token with object read/write/delete permissions for that bucket, then set those values in `.env` or deployment secrets and restart the backend. The backend uses `region: auto` for R2 through `config/backend/config.yaml`.

If S3-compatible storage is not configured, file upload, download, and delete return `503 storage_not_configured`; metadata listing still depends on the database but new uploads cannot succeed.

## First-Time Setup

1. Copy `.env.example` to `.env` and set `ROOT_PASSWORD`, `POSTGRES_PASSWORD`
   and `REDIS_PASSWORD`. For a deployment others will use, also set
   `SESSION_SECRET`, `CRYPTO_SECRET` and `FRONTEND_BASE_URL`, and fill `site`
   in `config/backend/config.yaml`.
2. Start the stack:

   ```bash
   docker compose --env-file .env -f platform/docker/docker-compose.yml up --build -d
   ```

3. On first start the gateway runs `/migrate` to create the Core and
   Telemetry schema, then `/migrate -command ledger-bootstrap` to open
   financial continuity (`LAZU_RUN_MIGRATIONS_ON_START` and
   `LAZU_BOOTSTRAP_LEDGER_ON_START`, both on in this compose file). On a
   running deployment both are no-ops. If the bootstrap refuses because Redis
   lost its ledger state, recover with `ledger-report` and `ledger-recover`
   before starting writers; it never guesses.
4. Open the console and sign in with `ROOT_USERNAME` / `ROOT_PASSWORD`.
5. Use `管理 / 设置` for operational values such as pricing, notices, ratios,
   toggles and the legal documents.

Deployments that run migrations as a separate release step leave both
variables off and run `/migrate`, then `/migrate -command ledger-bootstrap`
once, before the first writer starts.

When the hosted API and web console use separate domains, set, for example,
`FRONTEND_BASE_URL=https://lazu.ai`. GET/HEAD requests for the password-reset
callback `/user/reset` and the canonical wallet callbacks `/console/wallet`
and `/console/usage` redirect to the matching SPA routes while API paths and
unknown paths continue to return their normal responses.

## Changing Credentials

1. Edit `.env` or `config/backend/config.yaml`.
2. Recreate the backend and jobs containers with the updated environment:

   ```bash
   docker compose --env-file .env -f platform/docker/docker-compose.yml \
     up -d --force-recreate lazu-backend lazu-jobs
   ```

3. Verify that the recreated service is healthy at `/readyz`; test retained
   payment and email integrations from their normal operational workflows.

## Changing Operational Params

Change operational values directly in the console UI. These values are stored in the database `option` table and do not require a backend restart.

## Request content logging

`model_log_enabled` (environment: `MODEL_LOG_ENABLED`) is off by default. When
it is on, Lazu:

- appends one JSON line per model call to `data/export/<model_log_filename>`
  (rotated daily), holding the full response body or stream chunks together
  with the user, project and token ids, client IP, country, user agent and the
  agent session and thread ids;
- stores a preview of the first user message of each agent session, which the
  console's Sessions view shows and searches.

These files hold your users' content. Turn the log on only if you need it for
debugging or quality review, say so in your privacy policy, and keep the
`upload` target (if enabled) private. Local JSONL files are deleted after 7
days; anything uploaded follows the retention of the bucket it goes to.

## Retention And Disk Maintenance

Database request logs are retained for 30 days by default. Override this with `LOG_RETENTION_DAYS` and restart `lazu-backend` and `lazu-jobs` if your deployment needs a different window. Keep the value large enough for support and billing investigation, but small enough to prevent the `logs` table from growing without bound.

Set `LOG_RETENTION_INTERVAL_HOURS=24` so the cleanup runs daily from the application process. Do not install a separate SQL cron/timer for the same tables unless you intentionally disable application-managed retention.

Prune Docker images and build cache separately from application data, on a
schedule, and never prune volumes: PostgreSQL and Redis keep their data there.
For example, a daily `docker image prune -a --force --filter "until=168h"`
and `docker builder prune --force --filter "until=168h"` remove only what is
unused and older than seven days.
