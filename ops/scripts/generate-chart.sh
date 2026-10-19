#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT_DIR="${ROOT_DIR}/.local-results/repo-chart"
OUTPUT_FILE="${OUTPUT_DIR}/repo-file-counts.md"

mkdir -p "${OUTPUT_DIR}"
cd "${ROOT_DIR}"

go_files="$(git ls-files '*.go' | wc -l | tr -d ' ')"
web_files="$(git ls-files 'apps/web/src/*' 'apps/web/src/**/*' | wc -l | tr -d ' ')"
docs_files="$(git ls-files 'docs/*.md' 'docs/**/*.md' 'apps/docs/**/*' | wc -l | tr -d ' ')"

cat >"${OUTPUT_FILE}" <<EOF
pie title Repository file counts
  "Go" : ${go_files}
  "Web" : ${web_files}
  "Docs" : ${docs_files}
EOF

printf '[generate-chart] wrote %s\n' "${OUTPUT_FILE}"
