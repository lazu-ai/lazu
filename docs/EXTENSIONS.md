# Extension points

Some deployments compile in features this repository does not carry. They
attach through the hooks below; a build without extensions behaves exactly as
the core, and every hook is optional.

An extension is a set of files added to a checkout before it is built: Go
files in the core packages that register from `init()`, and web files under
`apps/web/src/extensions/<name>/`. Nothing in this repository names them.

## Backend (Go)

| Package | Hooks |
| --- | --- |
| `internal/model` (`extensions.go`) | Tables (`RegisterCoreModels`, `RegisterTelemetryModels`), schema steps (`RegisterMigration`), option defaults, media-job succeeded/failed/charged hooks and sources, top-up completing/reversed hooks, user and file deletion hooks, retention purges, hidden credentials, operated accounts, visit counts for the overview, home surfaces (`RegisterUserHomeSurface`) |
| `internal/controller` (`extensions.go`, `signup_hooks.go`) | Extra `/api/status` fields, cache policy for server-rendered pages, sign-up hooks, welcome mail per surface |
| `internal/router` (`extensions.go`) | Routes under `/api`, outside it, public pages per locale, and API-host redirects to the web app |
| `internal/middleware` (`extensions.go`) | Token resolvers for an extension's own signed calls |
| `internal/publicsite` | Model page actions (`RegisterModelAction`), shared templates (`RegisterBaseTemplates`); templates under `templates/` are embedded with the core's |
| `internal/worker`, `apps/worker` | Worker tasks (`RegisterTasks`), worker subcommands |
| `internal/common` | Config sections (`RegisterConfigSection`), background loops of the API process, client User-Agent patterns |

A build without extensions sees these hooks as functions nothing calls.
`make test-deadcode` fails on such functions unless `ops/extension-surface.txt`
lists them, so a change that adds a hook adds its line there too.

## Web app

`src/extensions/index.ts` finds `src/extensions/<name>/extension.tsx`
(routes, full-screen surfaces, 运营 tabs, sidebar entries, settings fields,
wallet and dashboard content, public header and footer links, public chrome
for server-rendered sections). The types are in `src/extensions/types.ts`.
Next to it:

- `i18n.ts` — locale namespaces, surface languages, client badges.
- `locales.json` — where the build finds the extension's locale files.
- `public-site.ts` — scripts and styles of server-rendered pages.
- `build.mjs` — build-time additions: chrome sections, `llms.txt` sections,
  sitemaps, prerender checks, dev-server proxy paths.

## Frontend image

`apps/web/nginx.d/` holds nginx templates (`http.d`, `server.d`, `page.d`)
and `apps/web/entrypoint.d/` entrypoint steps; see the README in each.
`platform/docker/Dockerfile` takes `EXTRA_APK_PACKAGES` for packages an
extension's backend needs.

## Docs site

Each `apps/docs/extensions/*.head.js` file is inlined as a `<script>` in every
docs page's `<head>`, in name order. The public tree ships none.
