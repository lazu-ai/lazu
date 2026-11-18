#!/usr/bin/env bash

# Shared helpers for docker compose command resolution.
# Supports both `docker compose` (v2 plugin) and legacy `docker-compose`.

if [[ -z "${COMPOSE_FILE:-}" ]]; then
  COMPOSE_FILE=""
fi

if [[ -z "${COMPOSE_ENV_FILE:-}" ]]; then
  COMPOSE_ENV_FILE=""
fi

if [[ -z "${COMPOSE_CMD_RESOLVED:-}" ]]; then
  COMPOSE_CMD_RESOLVED="false"
fi

resolve_compose_cmd() {
  if [[ "${COMPOSE_CMD_RESOLVED}" == "true" ]]; then
    return 0
  fi

  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    COMPOSE_BIN="docker"
    COMPOSE_SUBCMD="compose"
    COMPOSE_CMD_RESOLVED="true"
    return 0
  fi

  if command -v docker-compose >/dev/null 2>&1; then
    COMPOSE_BIN="docker-compose"
    COMPOSE_SUBCMD=""
    COMPOSE_CMD_RESOLVED="true"
    return 0
  fi

  echo "[compose] ERROR: neither 'docker compose' nor 'docker-compose' is available." >&2
  return 1
}

set_compose_file() {
  local root_dir="$1"
  COMPOSE_FILE="${root_dir}/platform/docker/docker-compose.yml"
}

# Local compose env vars live in the repo-root .env. Production compose uses
# .env.production through the deploy script. Call this from scripts that invoke `compose`
# so the helper passes `--env-file` explicitly — relying on compose's
# default lookup would point it at platform/docker/.env, which by
# project convention does not exist.
set_compose_env_file() {
  COMPOSE_ENV_FILE="$1"
}

# Resolve the local development .env file. Prefer ${root_dir}/.env, and when
# running inside a linked git worktree without its own .env, fall back to the
# main working tree's .env so worktrees can run ./run.sh without copying .env.
resolve_local_env_file() {
  local root_dir="$1"
  if [[ -f "${root_dir}/.env" ]]; then
    printf '%s\n' "${root_dir}/.env"
    return 0
  fi

  local common_git_dir=""
  common_git_dir="$(git -C "${root_dir}" rev-parse --git-common-dir 2>/dev/null || true)"
  if [[ -n "${common_git_dir}" ]]; then
    local main_root=""
    main_root="$(cd "${root_dir}" && cd "${common_git_dir}/.." 2>/dev/null && pwd || true)"
    if [[ -n "${main_root}" && -f "${main_root}/.env" ]]; then
      printf '%s\n' "${main_root}/.env"
      return 0
    fi
  fi

  return 1
}

compose_cmd_label() {
  resolve_compose_cmd
  if [[ -n "${COMPOSE_SUBCMD}" ]]; then
    echo "${COMPOSE_BIN} ${COMPOSE_SUBCMD}"
  else
    echo "${COMPOSE_BIN}"
  fi
}

compose() {
  resolve_compose_cmd
  if [[ -z "${COMPOSE_FILE}" ]]; then
    echo "[compose] ERROR: COMPOSE_FILE is not set." >&2
    return 1
  fi

  local env_flag=()
  if [[ -n "${COMPOSE_ENV_FILE}" && -f "${COMPOSE_ENV_FILE}" ]]; then
    env_flag=(--env-file "${COMPOSE_ENV_FILE}")
  fi

  if [[ -n "${COMPOSE_SUBCMD}" ]]; then
    "${COMPOSE_BIN}" "${COMPOSE_SUBCMD}" "${env_flag[@]}" -f "${COMPOSE_FILE}" "$@"
    return $?
  fi

  "${COMPOSE_BIN}" "${env_flag[@]}" -f "${COMPOSE_FILE}" "$@"
}
