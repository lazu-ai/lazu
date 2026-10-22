# syntax=docker/dockerfile:1.5@sha256:39b85bbfa7536a5feceb7372a0817649ecb2724562a38360f4d6a7782a409b14

FROM golang:1.26.10-alpine@sha256:5cb67f19c5e7994dcb80c074f7cfca8aeeada45bbdca035f73df269cefdcce61 AS builder
ENV GO111MODULE=on CGO_ENABLED=0 GOTOOLCHAIN=local

ARG TARGETOS
ARG TARGETARCH
ARG APP_VERSION=dev
ENV GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}

WORKDIR /build

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    MOD=$(go list -m) \
    && VER=${APP_VERSION} \
    && go build -ldflags "-s -w -X '${MOD}/internal/common.Version=${VER}'" -o lazu-api ./apps/api \
	&& go build -ldflags "-s -w -X '${MOD}/internal/common.Version=${VER}'" -o lazu-worker ./apps/worker \
	&& go build -ldflags "-s -w -X '${MOD}/internal/common.Version=${VER}'" -o lazu-migrate ./apps/migrate

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce

ARG APP_VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/lazu-ai/lazu" \
      org.opencontainers.image.version="${APP_VERSION}"

# ffmpeg: audio length for transcription billing. EXTRA_APK_PACKAGES adds
# packages a build's extensions need; of font-noto-cjk only the two regular
# CJK faces are kept.
ARG EXTRA_APK_PACKAGES=""
RUN apk upgrade --no-cache \
	&& apk add --no-cache ca-certificates tzdata ffmpeg postgresql16-client su-exec ${EXTRA_APK_PACKAGES} \
	&& if [ -d /usr/share/fonts/noto ]; then find /usr/share/fonts/noto -name '*CJK*' ! -name 'NotoSansCJK-Regular.ttc' ! -name 'NotoSerifCJK-Regular.ttc' -delete; fi \
	&& update-ca-certificates \
	&& addgroup -S lazu \
	&& adduser -S -D -H -G lazu lazu \
	&& mkdir -p /data \
	&& chown lazu:lazu /data

COPY --from=builder /build/lazu-api /
COPY --from=builder /build/lazu-worker /worker
COPY --from=builder /build/lazu-migrate /migrate
COPY --chmod=0555 platform/docker/entrypoint.sh /entrypoint.sh
EXPOSE 3000
WORKDIR /data
# Root is limited to the entrypoint's one-time legacy-volume ownership repair;
# the migration, API, and worker processes are exec'd as the lazu user.
USER root
ENTRYPOINT ["/entrypoint.sh"]
