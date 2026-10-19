#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

tracked_files="$(git ls-files | wc -l | tr -d ' ')"
go_files="$(git ls-files '*.go' | wc -l | tr -d ' ')"
web_files="$(git ls-files 'apps/web/src/*' 'apps/web/src/**/*' | wc -l | tr -d ' ')"
docs_files="$(git ls-files 'docs/*.md' 'docs/**/*.md' 'apps/docs/**/*' | wc -l | tr -d ' ')"

printf '[repo-stats] tracked_files=%s go_files=%s web_files=%s docs_files=%s\n' \
  "${tracked_files}" \
  "${go_files}" \
  "${web_files}" \
  "${docs_files}"
