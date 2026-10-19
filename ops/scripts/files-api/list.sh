#!/usr/bin/env bash
# List uploaded files. Optional purpose filter.
#
# Usage:
#   export LAZU_API_KEY="sk-..."
#   ./ops/scripts/files-api/list.sh                # all files
#   ./ops/scripts/files-api/list.sh vision         # filter by purpose

set -euo pipefail

BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:3000}"
LAZU_API_KEY="${LAZU_API_KEY:-}"

if [ -z "$LAZU_API_KEY" ]; then
  echo "LAZU_API_KEY is required" >&2
  exit 1
fi

query=""
if [ "$#" -ge 1 ] && [ -n "$1" ]; then
  query="?purpose=$1"
fi

curl -sS --fail-with-body \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  "${BACKEND_URL}/v1/files${query}" \
  | jq '.data | map({id, purpose, filename, bytes, created_at, status})'
