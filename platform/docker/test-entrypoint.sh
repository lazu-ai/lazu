#!/bin/sh
set -eu

image=${1:?usage: test-entrypoint.sh IMAGE}
volume="lazu-entrypoint-test-$$"

cleanup() {
  docker volume rm -f "$volume" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

docker volume create "$volume" >/dev/null
docker run --rm \
  -v "$volume:/data" \
  alpine:3.22 \
  /bin/sh -c 'mkdir -p /data/logs && touch /data/logs/legacy.jsonl && chown -R 0:0 /data'

docker run --rm \
  -v "$volume:/data" \
  "$image" \
  /bin/sh -c '
    id -u | grep -vx 0 >/dev/null
    test -w /data/logs/legacy.jsonl
    test -f /data/.lazu-owned-v1
    touch /data/logs/runtime.jsonl
  '
