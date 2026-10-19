#!/usr/bin/env bash
# Clean rebuild for local development
# Removes all caches, reinstalls dependencies, then starts dev servers

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}"
FRONT_DIR="${ROOT_DIR}/apps/web"
DOCS_DIR="${ROOT_DIR}/apps/docs"
print_help() {
  cat <<'EOF'
Usage: ./rebuild.sh

Options:
  --help, -h   Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)
      print_help
      exit 0
      ;;
    *)
      echo "[rebuild] Unknown option: $1" >&2
      print_help
      exit 1
      ;;
  esac
done

echo "=========================================="
echo "  Lazu - Clean Rebuild"
echo "=========================================="
echo "[rebuild] Workspace: ${ROOT_DIR}"
echo "[rebuild] Mode: clean"
echo ""

# Clean legacy local binaries to keep repository root tidy.
echo "[rebuild:clean] Removing root-local binaries..."
rm -f "${ROOT_DIR}/lazu"
rm -rf "${ROOT_DIR}/.runtime/bin"

# Clean Go cache
echo "[rebuild:clean] Removing Go build cache..."
go clean -cache 2>/dev/null || true
rm -rf "${ROOT_DIR}/.devcache"

# Clean frontend
echo "[rebuild:clean] Removing web and docs caches..."
rm -rf "${FRONT_DIR}/node_modules"
rm -rf "${FRONT_DIR}/.vite"
rm -rf "${FRONT_DIR}/dist"
rm -rf "${DOCS_DIR}/.next"
rm -rf "${DOCS_DIR}/.source"
rm -rf "${DOCS_DIR}/.generated"

# Reinstall Go dependencies
echo "[rebuild:deps] Downloading Go modules..."
(cd "${ROOT_DIR}" && go mod tidy && go mod download)

# Reinstall frontend dependencies
echo "[rebuild:deps] Installing web dependencies in apps/web..."
(cd "${FRONT_DIR}" && bun install --frozen-lockfile --save-text-lockfile --no-progress --no-summary)
rm -f "${FRONT_DIR}/bun.lockb"

echo "[rebuild:deps] Installing docs dependencies in apps/docs..."
(cd "${DOCS_DIR}" && npm ci --silent --no-audit --fund=false)

echo ""
echo "[rebuild:start] Clean rebuild complete. Launching local development stack..."
echo ""

# Start through the canonical quick entrypoint so both paths use the same
# single-file local environment and process orchestration.
exec "${SCRIPT_DIR}/run.sh"
