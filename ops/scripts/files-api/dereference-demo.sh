#!/usr/bin/env bash
# Demonstrate how to reference an uploaded file from /v1/responses.
# Uploads an image, then sends a /responses request that includes its
# file_id; the server's DereferenceFileIDs middleware will swap it for
# inline base64 before forwarding to the upstream model.
#
# Usage:
#   export LAZU_API_KEY="sk-..."
#   export DEREF_MODEL="gpt-4o-mini"      # or any model with vision
#   ./ops/scripts/files-api/dereference-demo.sh path/to/image.png

set -euo pipefail

BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:3000}"
LAZU_API_KEY="${LAZU_API_KEY:-}"
DEREF_MODEL="${DEREF_MODEL:-gpt-4o-mini}"

if [ -z "$LAZU_API_KEY" ]; then
  echo "LAZU_API_KEY is required" >&2
  exit 1
fi

if [ "$#" -lt 1 ]; then
  echo "Usage: $0 <image-path>" >&2
  exit 2
fi

img=$1
[ -f "$img" ] || { echo "File not found: $img" >&2; exit 2; }

# 1 · Upload as vision
echo "1) uploading $img as purpose=vision …"
fid=$(curl -sS --fail-with-body \
  -X POST \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  -F "file=@${img}" \
  -F "purpose=vision" \
  "${BACKEND_URL}/v1/files" | jq -r .id)
echo "   file_id = $fid"

# 2 · Reference it in /v1/responses
echo
echo "2) calling /v1/responses with file_id reference …"
payload=$(jq -n \
  --arg model "$DEREF_MODEL" \
  --arg fid   "$fid" \
  '{
    model: $model,
    input: [{
      role: "user",
      content: [
        { type: "input_text",  text: "Describe what you see in this image in one sentence." },
        { type: "input_image", file_id: $fid }
      ]
    }]
  }')

curl -sS --fail-with-body \
  -X POST \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  -H "Content-Type: application/json" \
  --data "$payload" \
  "${BACKEND_URL}/v1/responses" \
  | jq .

# 3 · Cleanup
echo
echo "3) deleting $fid …"
curl -sS --fail-with-body \
  -X DELETE \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  "${BACKEND_URL}/v1/files/${fid}" \
  | jq -r '.deleted | if . then "  ok" else "  no" end'
