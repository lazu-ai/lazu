#!/usr/bin/env bash
# One-shot helper to upload a single file. Prints the file_id on success.
#
# Usage:
#   export LAZU_API_KEY="sk-..."
#   ./ops/scripts/files-api/upload.sh path/to/file.pdf user_data
#   ./ops/scripts/files-api/upload.sh path/to/image.png vision

set -euo pipefail

BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:3000}"
LAZU_API_KEY="${LAZU_API_KEY:-}"

if [ -z "$LAZU_API_KEY" ]; then
  echo "LAZU_API_KEY is required" >&2
  exit 1
fi

if [ "$#" -lt 1 ]; then
  echo "Usage: $0 <file-path> [purpose=user_data]" >&2
  exit 2
fi

file=$1
purpose=${2:-user_data}

if [ ! -f "$file" ]; then
  echo "File not found: $file" >&2
  exit 2
fi

curl -sS --fail-with-body \
  -X POST \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  -F "file=@${file}" \
  -F "purpose=${purpose}" \
  "${BACKEND_URL}/v1/files" \
  | jq .
