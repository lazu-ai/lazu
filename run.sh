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
  .env              local development configuration
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

if [[ ! -f "${SCRIPT_DIR}/.env" ]]; then
  echo "[run] Missing ${SCRIPT_DIR}/.env. Create the local development environment file first." >&2
  exit 1
fi

echo "=========================================="
echo "  Lazu - Quick Local Start"
echo "=========================================="
echo "[run] Environment: .env"
echo "[run] Tip: use ./rebuild.sh for a clean dependency/cache rebuild."
echo ""

exec "${SCRIPT_DIR}/ops/scripts/dev.sh"
