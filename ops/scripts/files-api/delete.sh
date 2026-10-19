#!/usr/bin/env bash
# Delete a file by id, or delete all files matching a purpose with --all.
#
# Usage:
#   export LAZU_API_KEY="sk-..."
#   ./ops/scripts/files-api/delete.sh file-xxx                # delete one
#   ./ops/scripts/files-api/delete.sh --all user_data         # bulk by purpose
#   ./ops/scripts/files-api/delete.sh --all                   # bulk all (asks for confirm)

set -euo pipefail

BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:3000}"
LAZU_API_KEY="${LAZU_API_KEY:-}"

if [ -z "$LAZU_API_KEY" ]; then
  echo "LAZU_API_KEY is required" >&2
  exit 1
fi

delete_one() {
  local fid=$1
  printf 'delete %s … ' "$fid"
  curl -sS --fail-with-body \
    -X DELETE \
    -H "Authorization: Bearer ${LAZU_API_KEY}" \
    "${BACKEND_URL}/v1/files/${fid}" \
    | jq -r '.deleted | if . then "ok" else "no" end'
}

if [ "${1:-}" = "--all" ]; then
  purpose=${2:-}
  query=""
  [ -n "$purpose" ] && query="?purpose=$purpose"
  if [ -z "$purpose" ]; then
    read -p "Delete ALL files for this token? [y/N] " confirm
    [ "$confirm" = "y" ] || { echo "aborted"; exit 0; }
  fi
  ids=$(curl -sS \
    -H "Authorization: Bearer ${LAZU_API_KEY}" \
    "${BACKEND_URL}/v1/files${query}" | jq -r '.data[].id')
  if [ -z "$ids" ]; then
    echo "(no files)"
    exit 0
  fi
  for id in $ids; do
    delete_one "$id"
  done
  exit 0
fi

if [ "$#" -lt 1 ]; then
  echo "Usage: $0 <file-id>  |  $0 --all [purpose]" >&2
  exit 2
fi

delete_one "$1"
