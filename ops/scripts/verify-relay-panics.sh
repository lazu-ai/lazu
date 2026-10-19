#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

if rg -n -P '^[[:space:]]*panic\("implement me"\)' internal/relay/channel -g '*adaptor.go'; then
  echo "[relay] panic(\"implement me\") detected in adaptor implementations."
  echo "[relay] replace panic paths with explicit errors and guard via capability profile."
  exit 1
fi

echo "[relay] adaptor panic guard passed."
