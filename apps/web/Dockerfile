# syntax=docker/dockerfile:1.7

FROM oven/bun:1.3.14 AS build
WORKDIR /repo/apps/web
COPY apps/web/package.json apps/web/bun.lock ./
COPY apps/web/patches ./patches
COPY apps/web/vendor ./vendor
# Use frozen lockfile; if integrity check fails, clear cache and retry
RUN --mount=type=cache,target=/root/.bun/install/cache \
    bun install --frozen-lockfile || (rm -rf ~/.bun/install/cache && bun install --frozen-lockfile)
COPY brand /repo/brand
# SEO generation publishes localized Markdown siblings from the same legal
# defaults embedded by the API. Keep that single source of truth available in
# the frontend build stage; it is not copied into the final nginx image.
COPY internal/setting/system_setting/legal_defaults /repo/internal/setting/system_setting/legal_defaults
COPY apps/web /repo/apps/web
ENV DISABLE_ESLINT_PLUGIN=true
RUN bun run build

FROM nginx:alpine
RUN apk add --no-cache bash curl
COPY --from=build /repo/apps/web/dist /usr/share/nginx/html
# Templates are rendered at start; only LAZU_NGINX_* variables are substituted,
# preserving nginx's own $variables.
ENV NGINX_ENVSUBST_FILTER="^LAZU_NGINX_"
COPY apps/web/nginx.conf /etc/nginx/templates/default.conf.template
# Extension templates and entrypoint steps (apps/web/nginx.d, entrypoint.d).
COPY apps/web/nginx.d/ /etc/nginx/templates/lazu/
COPY --chmod=755 apps/web/entrypoint.d/ /docker-entrypoint.d/
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD curl -fsS http://localhost/ >/dev/null || exit 1
