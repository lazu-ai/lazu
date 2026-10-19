#!/usr/bin/env bash
# Diagnose and optionally rebuild frontend container to fix stale bundle issues.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# shellcheck source=ops/scripts/lib/compose.sh
source "${SCRIPT_DIR}/lib/compose.sh"
set_compose_file "${ROOT_DIR}"

REBUILD="false"

usage() {
  cat <<'EOF'
Usage: ./ops/scripts/frontend-doctor.sh [options]

Options:
  --rebuild                    Rebuild frontend image with --no-cache and restart service
  --compose-file <path>        Override compose file path (default: platform/docker/docker-compose.yml)
  -h, --help                   Show this help

Examples:
  ./ops/scripts/frontend-doctor.sh
  ./ops/scripts/frontend-doctor.sh --rebuild
  ./ops/scripts/frontend-doctor.sh --compose-file platform/docker/docker-compose.yml --rebuild
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --rebuild)
      REBUILD="true"
      shift
      ;;
    --compose-file)
      if [[ $# -lt 2 ]]; then
        echo "[doctor] Missing value for --compose-file" >&2
        exit 1
      fi
      COMPOSE_FILE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "[doctor] Unknown option: $1" >&2
      usage
      exit 1
      ;;
  esac
done

resolve_compose_cmd
echo "[doctor] Using: $(compose_cmd_label) -f ${COMPOSE_FILE}"
echo "[doctor] Repo: ${ROOT_DIR}"
echo "[doctor] Branch: $(git -C "${ROOT_DIR}" rev-parse --abbrev-ref HEAD)"
echo "[doctor] Commit: $(git -C "${ROOT_DIR}" rev-parse --short HEAD)"

if grep -q "react-components" "${ROOT_DIR}/apps/web/vite.config.js"; then
  echo "[doctor] WARN: source still contains 'react-components' in vite.config.js"
else
  echo "[doctor] OK: source vite.config.js has no 'react-components' chunk rule"
fi

if grep -q "scheduler" "${ROOT_DIR}/apps/web/vite.config.js"; then
  echo "[doctor] OK: source vite.config.js includes scheduler in chunk strategy"
else
  echo "[doctor] WARN: source vite.config.js does not mention scheduler in chunk strategy"
fi

if grep -Eq "^FROM oven/bun:1\\.([2-9]|[1-9][0-9])" "${ROOT_DIR}/apps/web/Dockerfile"; then
  echo "[doctor] OK: frontend Dockerfile uses bun >= 1.2"
else
  echo "[doctor] WARN: frontend Dockerfile is not bun >= 1.2 (possible old build behavior)"
fi

if [[ "${REBUILD}" == "true" ]]; then
  echo "[doctor] Rebuilding frontend image with no cache..."
  compose rm -sf lazu-frontend >/dev/null 2>&1 || true
  docker image rm -f lazu-frontend:local >/dev/null 2>&1 || true
  compose build --no-cache lazu-frontend
  compose up -d lazu-frontend
fi

if ! docker ps --format '{{.Names}}' | grep -qx "lazu-frontend"; then
  echo "[doctor] ERROR: container 'lazu-frontend' is not running." >&2
  echo "[doctor] Hint: run '$(compose_cmd_label) -f ${COMPOSE_FILE} up -d lazu-frontend' first." >&2
  exit 1
fi

echo "[doctor] Inspecting built JS assets in container..."
asset_js="$(compose exec -T lazu-frontend sh -lc 'ls -1 /usr/share/nginx/html/assets/*.js 2>/dev/null | sed "s|.*/||" | sort' || true)"
if [[ -z "${asset_js}" ]]; then
  echo "[doctor] ERROR: no JS assets found at /usr/share/nginx/html/assets" >&2
  exit 1
fi
echo "${asset_js}" | sed 's/^/[doctor]   /'

if echo "${asset_js}" | grep -Eq '^react-components-.*\.js$'; then
  echo "[doctor] ERROR: legacy react-components chunk still present. You are running an old frontend bundle." >&2
  exit 2
fi

index_refs="$(compose exec -T lazu-frontend sh -lc "grep -o 'assets/[^\" ]*\\.js' /usr/share/nginx/html/index.html | sort -u" || true)"
echo "[doctor] JS references in /index.html:"
if [[ -n "${index_refs}" ]]; then
  echo "${index_refs}" | sed 's/^/[doctor]   /'
else
  echo "[doctor]   (none)"
fi

if echo "${index_refs}" | grep -q "react-components-"; then
  echo "[doctor] ERROR: index.html still references react-components chunk. Deploy is stale." >&2
  exit 2
fi

echo "[doctor] OK: frontend bundle is from the new chunk strategy."
echo "[doctor] Next: hard-refresh browser (Ctrl/Cmd+Shift+R) and verify /assets no longer contains react-components-*."
