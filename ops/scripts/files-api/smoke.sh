#!/usr/bin/env bash
# End-to-end smoke test for the OpenAI-compatible Files API.
#
# Covers the full lifecycle (upload → list → retrieve → content → delete)
# for both supported purposes (user_data, vision), plus the dereference
# integration via /v1/responses, plus the main error paths
# (unauthorized, unsupported purpose, not-found).
#
# Usage:
#   export LAZU_API_KEY="sk-..."        # required, your channel/user token
#   export BACKEND_URL="http://127.0.0.1:3000"   # optional, defaults shown
#   export DEREF_MODEL="gpt-4o-mini"        # optional, model for /responses test (skip if unset)
#   ./ops/scripts/files-api/smoke.sh
#
# Exit code: 0 if all tests pass, 1 otherwise. Mirrors the layout of
# the canonical repo-root `run.sh` / `rebuild.sh` entrypoints.

set -uo pipefail

BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:3000}"
LAZU_API_KEY="${LAZU_API_KEY:-}"
DEREF_MODEL="${DEREF_MODEL:-}"
HTTP_TIMEOUT="${HTTP_TIMEOUT:-15}"
TMPDIR_=$(mktemp -d -t files-api-smoke.XXXXXX)
trap 'rm -rf "$TMPDIR_"' EXIT

pass_count=0
fail_count=0
created_file_ids=()

if [ -z "$LAZU_API_KEY" ]; then
  echo "❌  LAZU_API_KEY is required. Export an sk-... token and retry."
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "❌  jq is required for JSON parsing. Install it (brew install jq) and retry."
  exit 1
fi

# ─────────────────────────────────────────────────────────────────────
# Helpers
# ─────────────────────────────────────────────────────────────────────

color() { printf '\033[%sm%s\033[0m' "$1" "$2"; }
ok() { color "32" "PASS"; }
ko() { color "31" "FAIL"; }
dim() { color "90" "$1"; }

assert_eq() {
  local label=$1 actual=$2 expected=$3
  if [ "$actual" = "$expected" ]; then
    echo "  $(ok) $label  $(dim "(=$expected)")"
    pass_count=$((pass_count + 1))
  else
    echo "  $(ko) $label  expected=$expected  actual=$actual"
    fail_count=$((fail_count + 1))
  fi
}

assert_truthy() {
  local label=$1 actual=$2
  if [ -n "$actual" ] && [ "$actual" != "null" ]; then
    echo "  $(ok) $label  $(dim "(=$actual)")"
    pass_count=$((pass_count + 1))
  else
    echo "  $(ko) $label  empty/null"
    fail_count=$((fail_count + 1))
  fi
}

section() { echo; color "1;36" "── $1"; echo; }

# Curl invocation that captures body + status code on separate lines.
# Echoes: <body>\n<status>
api() {
  local method=$1 path=$2
  shift 2
  curl -sS --max-time "$HTTP_TIMEOUT" \
    -w '\n%{http_code}' \
    -X "$method" \
    -H "Authorization: Bearer ${LAZU_API_KEY}" \
    "$@" \
    "${BACKEND_URL}${path}"
}

split_body_status() {
  local out=$1
  body=$(printf '%s' "$out" | sed '$d')
  status=$(printf '%s' "$out" | tail -n1)
}

# ─────────────────────────────────────────────────────────────────────
# Fixtures
# ─────────────────────────────────────────────────────────────────────

# A tiny text fixture for purpose=user_data
echo "Hello from lazu files API smoke test. Run at $(date -u +%FT%TZ)." \
  > "$TMPDIR_/sample.txt"
TEXT_SIZE=$(wc -c < "$TMPDIR_/sample.txt" | tr -d ' ')
TEXT_SHA=$(shasum -a 256 "$TMPDIR_/sample.txt" | awk '{print $1}')

# A minimal 1×1 transparent PNG (89 bytes) for purpose=vision.
# Base64 from `echo -n iVBORw0KGgo... | base64 -d > tiny.png`.
python3 - <<'PY' > "$TMPDIR_/tiny.png"
import base64, sys
b = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
)
sys.stdout.buffer.write(b)
PY
PNG_SIZE=$(wc -c < "$TMPDIR_/tiny.png" | tr -d ' ')

# ─────────────────────────────────────────────────────────────────────
# Pre-flight
# ─────────────────────────────────────────────────────────────────────

section "0 · pre-flight"
status_out=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' "${BACKEND_URL}/api/status" || true)
if [ "$status_out" != "200" ]; then
  echo "  $(ko) backend not reachable at $BACKEND_URL (status=$status_out)"
  echo "  Start the server first: ./rebuild.sh --fast"
  exit 1
fi
echo "  $(ok) backend reachable at $BACKEND_URL"
echo "  $(dim "key prefix"): ${LAZU_API_KEY:0:8}…"

# ─────────────────────────────────────────────────────────────────────
# 1 · Upload user_data
# ─────────────────────────────────────────────────────────────────────

section "1 · POST /v1/files (purpose=user_data)"
out=$(api POST /v1/files \
  -F "file=@${TMPDIR_}/sample.txt" \
  -F "purpose=user_data")
split_body_status "$out"

# Early bail-out for the common "didn't configure R2/COS yet" case:
# all 22 assertions would fail with the same root cause, so we surface
# the actionable message instead of drowning the user in red.
if [ "$status" = "503" ]; then
  err_code=$(echo "$body" | jq -r '.error.code // "unknown"' 2>/dev/null)
  if [ "$err_code" = "storage_not_configured" ] || \
     echo "$body" | grep -qi "storage"; then
    echo "  $(ko) backend returned 503 storage_not_configured"
    echo
    echo "  Object storage (R2/S3/COS) is not configured on this backend."
    echo "  See ops/scripts/files-api/README.md §0 for setup, or"
    echo "  docs/DEPLOYMENT.md for the full deployment checklist."
    echo
    echo "  Quick check: \`grep R2_ ${BACKEND_URL%:*}/.env\` should show 4 R2_ vars."
    exit 2
  fi
fi

assert_eq "status code" "$status" "201"
USER_FILE_ID=$(echo "$body" | jq -r '.id // empty')
assert_truthy "file id"  "$USER_FILE_ID"
assert_eq    "object"   "$(echo "$body" | jq -r '.object')" "file"
assert_eq    "purpose"  "$(echo "$body" | jq -r '.purpose')" "user_data"
assert_eq    "bytes"    "$(echo "$body" | jq -r '.bytes')"   "$TEXT_SIZE"
assert_eq    "status"   "$(echo "$body" | jq -r '.status')"  "processed"
created_file_ids+=("$USER_FILE_ID")

# ─────────────────────────────────────────────────────────────────────
# 2 · Upload vision (PNG)
# ─────────────────────────────────────────────────────────────────────

section "2 · POST /v1/files (purpose=vision)"
out=$(api POST /v1/files \
  -F "file=@${TMPDIR_}/tiny.png" \
  -F "purpose=vision")
split_body_status "$out"
assert_eq "status code" "$status" "201"
VISION_FILE_ID=$(echo "$body" | jq -r '.id // empty')
assert_truthy "file id" "$VISION_FILE_ID"
assert_eq    "purpose" "$(echo "$body" | jq -r '.purpose')" "vision"
assert_eq    "bytes"   "$(echo "$body" | jq -r '.bytes')"   "$PNG_SIZE"
created_file_ids+=("$VISION_FILE_ID")

# ─────────────────────────────────────────────────────────────────────
# 3 · List (no filter, then with purpose filter)
# ─────────────────────────────────────────────────────────────────────

section "3 · GET /v1/files"
out=$(api GET /v1/files)
split_body_status "$out"
assert_eq "status code" "$status" "200"
assert_eq "object" "$(echo "$body" | jq -r '.object')" "list"
found_user=$(echo "$body" | jq -r --arg id "$USER_FILE_ID" '.data | map(.id) | index($id) // ""')
found_vision=$(echo "$body" | jq -r --arg id "$VISION_FILE_ID" '.data | map(.id) | index($id) // ""')
assert_truthy "user_data file in list" "$found_user"
assert_truthy "vision file in list"   "$found_vision"

section "3a · GET /v1/files?purpose=vision"
out=$(api GET "/v1/files?purpose=vision")
split_body_status "$out"
assert_eq "status code" "$status" "200"
only_vision=$(echo "$body" | jq -r '[.data[] | .purpose] | unique | join(",")')
assert_eq "all entries are vision" "$only_vision" "vision"

# ─────────────────────────────────────────────────────────────────────
# 4 · Retrieve metadata
# ─────────────────────────────────────────────────────────────────────

section "4 · GET /v1/files/:id"
out=$(api GET "/v1/files/${USER_FILE_ID}")
split_body_status "$out"
assert_eq "status code" "$status" "200"
assert_eq "id matches" "$(echo "$body" | jq -r '.id')" "$USER_FILE_ID"
assert_eq "filename"   "$(echo "$body" | jq -r '.filename')" "sample.txt"

# ─────────────────────────────────────────────────────────────────────
# 5 · Download content + checksum
# ─────────────────────────────────────────────────────────────────────

section "5 · GET /v1/files/:id/content"
http_code=$(curl -sS --max-time "$HTTP_TIMEOUT" -o "$TMPDIR_/downloaded.txt" \
  -w '%{http_code}' \
  -H "Authorization: Bearer ${LAZU_API_KEY}" \
  "${BACKEND_URL}/v1/files/${USER_FILE_ID}/content")
assert_eq "status code" "$http_code" "200"
downloaded_sha=$(shasum -a 256 "$TMPDIR_/downloaded.txt" | awk '{print $1}')
assert_eq "sha256 matches upload" "$downloaded_sha" "$TEXT_SHA"

# ─────────────────────────────────────────────────────────────────────
# 6 · Error paths
# ─────────────────────────────────────────────────────────────────────

section "6 · errors — unauthorized"
out=$(curl -sS --max-time 5 -w '\n%{http_code}' "${BACKEND_URL}/v1/files")
split_body_status "$out"
assert_eq "no-auth → 401" "$status" "401"

section "6a · errors — unsupported purpose"
out=$(api POST /v1/files \
  -F "file=@${TMPDIR_}/sample.txt" \
  -F "purpose=fine-tune")
split_body_status "$out"
assert_eq "fine-tune → 400" "$status" "400"
err_code=$(echo "$body" | jq -r '.error.code // empty')
assert_truthy "error.code present" "$err_code"

section "6b · errors — retrieve non-existent"
out=$(api GET "/v1/files/file-doesnotexist00000000")
split_body_status "$out"
assert_eq "not found → 404" "$status" "404"

# ─────────────────────────────────────────────────────────────────────
# 7 · Dereference in /v1/responses (optional — skipped if no model)
# ─────────────────────────────────────────────────────────────────────

if [ -n "$DEREF_MODEL" ]; then
  section "7 · POST /v1/responses with file_id (model=$DEREF_MODEL)"
  body_json=$(jq -n \
    --arg model "$DEREF_MODEL" \
    --arg fid   "$VISION_FILE_ID" \
    '{
      model: $model,
      input: [{
        role: "user",
        content: [
          { type: "input_text",  text: "What is in this image?" },
          { type: "input_image", file_id: $fid }
        ]
      }]
    }')
  out=$(api POST /v1/responses \
    -H "Content-Type: application/json" \
    --data "$body_json")
  split_body_status "$out"
  # Pass if 2xx OR (404/400 with file-related error message) — we are
  # mainly checking the dereference middleware doesn't crash.
  if [ "$status" = "200" ] || [ "$status" = "201" ]; then
    echo "  $(ok) /responses accepted file_id"
    pass_count=$((pass_count + 1))
  else
    echo "  $(ko) /responses returned status=$status"
    echo "       body: $(echo "$body" | head -c 200)"
    fail_count=$((fail_count + 1))
  fi
else
  echo
  echo "  $(dim "[skipped]") set DEREF_MODEL=<model-name> to test /v1/responses dereference"
fi

# ─────────────────────────────────────────────────────────────────────
# 8 · Delete (cleanup) + verify gone
# ─────────────────────────────────────────────────────────────────────

section "8 · DELETE /v1/files/:id"
for fid in "${created_file_ids[@]}"; do
  out=$(api DELETE "/v1/files/${fid}")
  split_body_status "$out"
  assert_eq "delete $fid → 200" "$status" "200"
  assert_eq "deleted=true"      "$(echo "$body" | jq -r '.deleted')" "true"
done

section "8a · verify deletion"
out=$(api GET "/v1/files/${USER_FILE_ID}")
split_body_status "$out"
assert_eq "deleted file → 404" "$status" "404"

# ─────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────

echo
total=$((pass_count + fail_count))
if [ "$fail_count" -eq 0 ]; then
  echo "$(color "1;32" "✔ all $total assertions passed")"
  exit 0
else
  echo "$(color "1;31" "✘ $fail_count of $total assertions failed")"
  exit 1
fi
