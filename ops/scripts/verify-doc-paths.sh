#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

EXCLUDES=(
  --glob '!code-cleanup-tasks.md'
  --glob '!relay-provider-cleanup.md'
)

legacy_path_pattern='deploy/docker|(?<!ops/)scripts/(dev\.sh|stack\.sh|lib/compose\.sh|db_backup|dns)|(?<!apps/)web/src|config/config\.yaml|config/\.env\.example'
legacy_code_path_pattern='`(controller|service|model|router|relay|middleware|common|setting|dto|worker)/'
legacy_comment_path_pattern='(^|[[:space:]])//[[:space:]]*(controller|service|model|router|relay|middleware|common|setting|dto|worker)/'

has_legacy=0

if rg -n -P -e "${legacy_path_pattern}" docs "${EXCLUDES[@]}"; then
  has_legacy=1
fi

if rg -n -S -e "${legacy_code_path_pattern}" docs "${EXCLUDES[@]}"; then
  has_legacy=1
fi

if rg -n -P "${legacy_comment_path_pattern}" docs "${EXCLUDES[@]}"; then
  has_legacy=1
fi

if [[ "${has_legacy}" -ne 0 ]]; then
  echo "[docs] legacy path references detected in active docs."
  echo "[docs] migrate to apps/internal/ops/platform/config paths or move doc to historical allowlist."
  exit 1
fi

echo "[docs] documentation path references are consistent."

python3 ops/scripts/verify-doc-links.py
