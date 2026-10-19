#!/usr/bin/env bash
# Unified Docker stack management for Lazu.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# shellcheck source=ops/scripts/lib/compose.sh
source "${SCRIPT_DIR}/lib/compose.sh"
set_compose_file "${ROOT_DIR}"
set_compose_env_file "${ROOT_DIR}/.env"

usage() {
  cat <<'EOF'
Usage: ./ops/scripts/stack.sh <command> [options]

Commands:
  up [--build] [service ...]  Start the local Docker stack
  down                         Stop stack
  restart [--build]           Restart the local Docker stack
  ps                           Show stack status
  logs [service]               Follow logs (all services by default)
  clean [--yes] [--rmi-local]  Stop stack, remove volumes, and purge local data dirs
  help                         Show this help

Examples:
  ./ops/scripts/stack.sh up --build
  ./ops/scripts/stack.sh logs lazu-backend
  ./ops/scripts/stack.sh clean --yes
EOF
}

require_compose() {
  resolve_compose_cmd
  echo "[stack] Using: $(compose_cmd_label)"
}

parse_up_options() {
  BUILD_FLAG=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --build|-b)
        BUILD_FLAG="--build"
        shift
        ;;
      *)
        break
        ;;
    esac
  done
  REMAINING_ARGS=("$@")
}

confirm_clean() {
  local assume_yes="$1"
  if [[ "${assume_yes}" == "true" ]]; then
    return 0
  fi

  echo "WARNING: This will delete local runtime data:"
  echo "  - .runtime/data/"
  echo "  - .runtime/redis_data/"
  echo "  - .runtime/tmp/"
  read -r -p "Continue? (y/N): " confirm
  [[ "${confirm}" == "y" || "${confirm}" == "Y" ]]
}

command="${1:-help}"
if [[ $# -gt 0 ]]; then
  shift
fi

case "${command}" in
  up)
    require_compose
    parse_up_options "$@"
    if [[ ${#REMAINING_ARGS[@]} -eq 0 ]]; then
      compose up -d ${BUILD_FLAG}
    else
      compose up -d ${BUILD_FLAG} "${REMAINING_ARGS[@]}"
    fi
    ;;

  down)
    require_compose
    compose down
    ;;

  restart)
    require_compose
    parse_up_options "$@"
    compose down
    compose up -d ${BUILD_FLAG}
    ;;

  ps|status)
    require_compose
    compose ps
    ;;

  logs)
    require_compose
    if [[ $# -gt 0 ]]; then
      compose logs -f "$1"
    else
      compose logs -f
    fi
    ;;

  clean)
    require_compose
    assume_yes="false"
    remove_images="false"
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --yes|-y)
          assume_yes="true"
          ;;
        --rmi-local)
          remove_images="true"
          ;;
        --help|-h)
          usage
          exit 0
          ;;
        *)
          echo "[stack] Unknown option for clean: $1" >&2
          exit 1
          ;;
      esac
      shift
    done

    if ! confirm_clean "${assume_yes}"; then
      echo "[stack] Aborted."
      exit 0
    fi

    down_args=(down --volumes --remove-orphans)
    if [[ "${remove_images}" == "true" ]]; then
      down_args+=(--rmi local)
    fi
    compose "${down_args[@]}" || true

    rm -rf \
      "${ROOT_DIR}/.runtime/redis_data" \
      "${ROOT_DIR}/.runtime/data" \
      "${ROOT_DIR}/.runtime/tmp"
    echo "[stack] Clean completed."
    ;;

  help|--help|-h)
    usage
    ;;

  *)
    echo "[stack] Unknown command: ${command}" >&2
    usage
    exit 1
    ;;
esac
