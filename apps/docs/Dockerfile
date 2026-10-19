# syntax=docker/dockerfile:1.7

FROM node:22-alpine AS build
WORKDIR /repo
RUN apk add --no-cache git
COPY apps/docs/package.json apps/docs/package-lock.json ./apps/docs/
WORKDIR /repo/apps/docs
RUN --mount=type=cache,target=/root/.npm npm ci
WORKDIR /repo
COPY apps/docs ./apps/docs
COPY brand ./brand
COPY packages/contracts/openapi ./packages/contracts/openapi
WORKDIR /repo/apps/docs
RUN npm run build

FROM nginx:1.27-alpine AS runner
COPY apps/docs/nginx.conf /etc/nginx/conf.d/default.conf
# The site is built with basePath "/docs" and served under that prefix
# (nginx.conf), so the build lands in html/docs.
COPY --from=build /repo/apps/docs/dist /usr/share/nginx/html/docs

EXPOSE 80
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1/docs >/dev/null || exit 1
