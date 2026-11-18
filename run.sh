#!/usr/bin/env bash
# Quick local development entrypoint.
# Loads the local .env and starts PostgreSQL, Redis, the Go API, Vite, and docs.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  cat <<'EOF'
Usage: ./run.sh

Starts the local development stack without clearing caches or reinstalling
dependencies. Use ./rebuild.sh when dependencies or build caches need a clean
reset.

Environment:
  .env              local development configuration (or the main git working
                    tree's .env when running inside a linked git worktree)
  .env.production   production configuration for the production stack only
EOF
}

case "${1:-}" in
  "") ;;
  --help|-h)
    usage
    exit 0
    ;;
  *)
    echo "[run] Unknown option: $1" >&2
    usage >&2
    exit 1
    ;;
esac

# shellcheck source=ops/scripts/lib/compose.sh
source "${SCRIPT_DIR}/ops/scripts/lib/compose.sh"

ENV_FILE="$(resolve_local_env_file "${SCRIPT_DIR}" || true)"
if [[ -z "${ENV_FILE}" ]]; then
  echo "[run] Missing ${SCRIPT_DIR}/.env. Copy .env.example to .env first." >&2
  exit 1
fi

export LAZU_ENV_FILE="${ENV_FILE}"

echo "=========================================="
echo "  Lazu - Quick Local Start"
echo "=========================================="
echo "[run] Environment: ${ENV_FILE}"
echo "[run] Tip: use ./rebuild.sh for a clean dependency/cache rebuild."
echo ""

exec "${SCRIPT_DIR}/ops/scripts/dev.sh"
