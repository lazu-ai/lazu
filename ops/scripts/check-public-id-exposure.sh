#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

pattern='"(id|user_id|token_id|channel_id|vendor_id)"[[:space:]]*:[[:space:]]*[^,}]*\.(Id|ID)\b'

if grep -RInE --include='*.go' --exclude='*_test.go' "${pattern}" internal/controller; then
  echo "raw integer id exposure found in controller response payloads" >&2
  exit 1
fi
