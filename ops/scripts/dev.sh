#!/usr/bin/env bash
# Local development with Docker database + Redis (not SQLite)
# Starts DB/Redis via Docker, then runs Go backend + Vite frontend locally.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Load the single local development environment file.
# Production uses .env.production through the production compose/deploy path.
if [[ -f "${ROOT_DIR}/.env" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "${ROOT_DIR}/.env"
  set +a
fi

FRONT_DIR="${ROOT_DIR}/apps/web"
DOCS_DIR="${ROOT_DIR}/apps/docs"
RUNTIME_DIR="${ROOT_DIR}/.runtime"
RUNTIME_DATA_DIR="${RUNTIME_DIR}/data"
RUNTIME_TMP_DIR="${RUNTIME_DIR}/tmp"
FRONTEND_PORT="${LAZU_FRONTEND_PORT:-5173}"
DOCS_PORT="${LAZU_DOCS_PORT:-3002}"

# shellcheck source=ops/scripts/lib/compose.sh
source "${SCRIPT_DIR}/lib/compose.sh"
set_compose_file "${ROOT_DIR}"
set_compose_env_file "${ROOT_DIR}/.env"

echo "=========================================="
echo "  Lazu - Local Development Stack"
echo "=========================================="
echo "[dev] Workspace: ${ROOT_DIR}"
echo "[dev] Ports: backend 3000, web ${FRONTEND_PORT}, docs ${DOCS_PORT}"
echo ""

backend_pid=""
frontend_pid=""
docs_pid=""
db_service=""
db_label=""
db_dsn=""
db_host_port=""
postgres_password="${POSTGRES_PASSWORD:-lazu-local-postgres}"
redis_password="${REDIS_PASSWORD:-lazu-local-redis}"

kill_port_listener() {
  local port="$1"
  local label="$2"
  local pids
  pids="$(lsof -nP -t -iTCP:${port} -sTCP:LISTEN 2>/dev/null || true)"
  pids="${pids//$'\n'/ }"
  if [[ -z "${pids// /}" ]]; then
    return 0
  fi

  echo "[dev] Found existing ${label} listener on :${port}, stopping: ${pids}"
  kill ${pids} 2>/dev/null || true
  sleep 1

  local remained
  remained="$(lsof -nP -t -iTCP:${port} -sTCP:LISTEN 2>/dev/null || true)"
  remained="${remained//$'\n'/ }"
  if [[ -n "${remained// /}" ]]; then
    echo "[dev] Force stopping remaining ${label} listener on :${port}: ${remained}"
    kill -9 ${remained} 2>/dev/null || true
  fi
}

ensure_process_alive() {
  local pid="$1"
  local label="$2"
  local wait_seconds="$3"
  sleep "${wait_seconds}"
  if ! kill -0 "${pid}" 2>/dev/null; then
    echo "[dev] ERROR: ${label} failed to start. Please check logs above." >&2
    exit 1
  fi
}

wait_for_http() {
  local url="$1"
  local label="$2"
  local max_attempts="${3:-60}"
  local process_pid="${4:-}"
  local attempt
  for ((attempt = 1; attempt <= max_attempts; attempt++)); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi

    if [[ -n "${process_pid}" ]] && ! kill -0 "${process_pid}" 2>/dev/null; then
      local process_status=0
      wait "${process_pid}" || process_status=$?
      if [[ "${process_status}" -eq 0 ]]; then
        process_status=1
      fi
      echo "[dev] ERROR: ${label} process exited with status ${process_status} before becoming ready at ${url}." >&2
      return "${process_status}"
    fi
    sleep 1
  done

  echo "[dev] ERROR: ${label} did not become ready at ${url} within ${max_attempts}s." >&2
  exit 1
}

wait_for_redis() {
  local attempt
  for attempt in {1..60}; do
    if docker exec lazu-redis redis-cli --no-auth-warning -a "${redis_password}" --raw PING 2>/dev/null | grep -Fxq "PONG"; then
      return 0
    fi
    sleep 1
  done

  echo "[dev] ERROR: Redis did not accept the configured local password in time." >&2
  echo "[dev] If this is an old local container, recreate lazu-redis or set REDIS_PASSWORD to the existing local password." >&2
  docker logs --tail 40 lazu-redis >&2 || true
  exit 1
}

sync_postgres_password() {
  printf "ALTER USER lazu WITH PASSWORD :'postgres_password';\n" |
    docker exec -i "${db_service}" psql \
      -U lazu \
      -d lazu_app \
      -v ON_ERROR_STOP=1 \
      -v postgres_password="${postgres_password}" >/dev/null 2>&1
}

postgres_host_auth_check() {
  if command -v psql >/dev/null 2>&1; then
    PGPASSWORD="${postgres_password}" psql \
      -h 127.0.0.1 \
      -p 10000 \
      -U lazu \
      -d lazu_app \
      -tAc "select 1" >/dev/null 2>&1
    return
  fi

  docker exec -e PGPASSWORD="${postgres_password}" "${db_service}" psql \
    -h host.docker.internal \
    -p 10000 \
    -U lazu \
    -d lazu_app \
    -tAc "select 1" >/dev/null 2>&1
}

wait_for_postgres() {
  local attempt
  for attempt in {1..60}; do
    if docker exec "${db_service}" pg_isready -U lazu >/dev/null 2>&1; then
      sync_postgres_password || true
      if postgres_host_auth_check; then
        return 0
      fi
    fi
    sleep 1
  done

  echo "[dev] ERROR: PostgreSQL did not accept the configured local password through 127.0.0.1:10000 in time." >&2
  echo "[dev] If this is an old local volume, retry dev.sh so it can resync the local role password, or reset the local postgres volume after backing up any needed data." >&2
  docker logs --tail 40 "${db_service}" >&2 || true
  exit 1
}

kill_process_tree() {
  local pid="$1"
  [[ -z "${pid}" ]] && return 0
  if ! kill -0 "${pid}" 2>/dev/null; then
    return 0
  fi

  local children
  children="$(pgrep -P "${pid}" 2>/dev/null || true)"
  local child
  for child in ${children}; do
    kill_process_tree "${child}"
  done
  kill "${pid}" 2>/dev/null || true
}

cleanup() {
  local exit_status=$?
  trap - EXIT INT TERM
  echo ""
  echo "[dev] Shutting down..."
  [[ -n "${backend_pid}" ]] && kill_process_tree "${backend_pid}" || true
  [[ -n "${frontend_pid}" ]] && kill_process_tree "${frontend_pid}" || true
  [[ -n "${docs_pid}" ]] && kill_process_tree "${docs_pid}" || true
  exit "${exit_status}"
}

trap cleanup EXIT INT TERM

# Read compose services once so we can surface the real compose error when parsing fails.
compose_services="$(compose config --services 2>&1)" || {
  echo "[dev] ERROR: failed to parse services from ${COMPOSE_FILE}." >&2
  echo "${compose_services}" >&2
  exit 1
}

# The local compose stack has one database service: PostgreSQL.
if printf '%s\n' "${compose_services}" | grep -Fxq "lazu-postgres"; then
  db_service="lazu-postgres"
  db_label="PostgreSQL"
  db_dsn="postgresql://lazu:${postgres_password}@127.0.0.1:10000/lazu_app?sslmode=disable"
  db_host_port="localhost:10000"
else
  echo "[dev] ERROR: no supported database service found in ${COMPOSE_FILE}." >&2
  echo "[dev] Available services:" >&2
  printf '%s\n' "${compose_services}" | sed 's/^/[dev]   - /' >&2
  echo "[dev] Expected: lazu-postgres" >&2
  exit 1
fi

# Start DB and Redis containers (if not already running).
echo "[dev] Starting ${db_label} and Redis containers..."
compose up -d "${db_service}" lazu-redis lazu-telemetry

# Wait for Redis and DB to be ready with the same credentials used by local processes.
echo "[dev] Waiting for Redis to be ready..."
wait_for_redis
echo "[dev] Redis is ready!"

echo "[dev] Waiting for ${db_label} to be ready..."
wait_for_postgres
echo "[dev] ${db_label} is ready!"

# The telemetry store has its own volume and database identity. This works for
# existing Core volumes too; no initdb-only hook is relied on.
telemetry_ready=false
for attempt in {1..60}; do
  if docker exec lazu-telemetry pg_isready -U lazu -d lazu_telemetry >/dev/null 2>&1; then
    printf "ALTER USER lazu WITH PASSWORD :'postgres_password';\n" |
      docker exec -i lazu-telemetry psql -U lazu -d lazu_telemetry -v ON_ERROR_STOP=1 -v postgres_password="${postgres_password}" >/dev/null
    telemetry_ready=true
    break
  fi
  sleep 1
done
if [[ "${telemetry_ready}" != true ]]; then
  echo "[dev] ERROR: telemetry PostgreSQL did not become ready." >&2
  exit 1
fi

# Ensure runtime directories exist under .runtime/
mkdir -p "${RUNTIME_DATA_DIR}" "${RUNTIME_TMP_DIR}"

# Prepare a runtime-local app config to keep local outputs under .runtime/data.
runtime_config_file="${RUNTIME_TMP_DIR}/config.dev.runtime.yaml"
source_config_file=""
for candidate in "${ROOT_DIR}/config/backend/config.yaml" "${ROOT_DIR}/config/backend/config.yml"; do
  if [[ -f "${candidate}" ]]; then
    source_config_file="${candidate}"
    break
  fi
done
if [[ -n "${source_config_file}" ]]; then
  cp "${source_config_file}" "${runtime_config_file}"
  # Replace the first data dir declaration only.
  sed -E -i.bak '0,/^[[:space:]]*dir:[[:space:]]*\.\/data[[:space:]]*$/s//  dir: .\/.runtime\/data/' "${runtime_config_file}"
  rm -f "${runtime_config_file}.bak"
fi

# Install dependencies if needed
if [[ ! -d "${FRONT_DIR}/node_modules" ]]; then
  echo "[dev:deps] Installing web dependencies in apps/web..."
  (cd "${FRONT_DIR}" && bun install --frozen-lockfile --save-text-lockfile --no-progress --no-summary)
  rm -f "${FRONT_DIR}/bun.lockb"
fi

# Ensure stale local processes don't keep old code running.
kill_port_listener 3000 "backend"
kill_port_listener "${DOCS_PORT}" "docs"
kill_port_listener "${FRONTEND_PORT}" "web app"

# Start backend with detected DB.
echo "[dev:backend] Starting Go backend (apps/api) at http://localhost:3000 using ${db_label}..."
(
  cd "${ROOT_DIR}"
  export LOG_LEVEL="${LOG_LEVEL:-warn}"
  export ACCESS_LOG="${ACCESS_LOG:-false}"
  export GORM_LOG_LEVEL="${GORM_LOG_LEVEL:-error}"
  export GORM_SLOW_THRESHOLD_MS="${GORM_SLOW_THRESHOLD_MS:-1000}"
  export DATABASE_URL="${db_dsn}"
  export TELEMETRY_DATABASE_URL="postgresql://lazu:${postgres_password}@127.0.0.1:10002/lazu_telemetry?sslmode=disable"
  export PROCESS_ROLE=all-in-one
  export ROOT_USERNAME="${ROOT_USERNAME:-root}"
  export ROOT_PASSWORD="${ROOT_PASSWORD:-change-me-local-root}"
  export REDIS_CONN_STRING="redis://default:${redis_password}@127.0.0.1:10001/0"
  export FRONTEND_BASE_URL="${FRONTEND_BASE_URL:-http://localhost:${FRONTEND_PORT}}"
  export ERROR_LOG_ENABLED=true  # Enable error log capture.
  export LOG_RETENTION_DAYS=0    # Disable local log retention cleanup.
  export PRICING_SNAPSHOT_ENABLED="${PRICING_SNAPSHOT_ENABLED:-true}"
  export RELAY_EVENT_QUEUE_ENABLED="${RELAY_EVENT_QUEUE_ENABLED:-true}"
  if [[ -f "${runtime_config_file}" ]]; then
    export APP_CONFIG_FILE="${runtime_config_file}"
  fi
  go run ./apps/api
) &
backend_pid=$!
ensure_process_alive "${backend_pid}" "Go backend" 2
wait_for_http "http://127.0.0.1:3000/readyz" "backend readiness" "${LAZU_API_READY_TIMEOUT_SECONDS:-180}" "${backend_pid}"
echo "[dev:backend] Backend is ready."

# Start public docs app (served under /docs). The web app's Vite dev server proxies /docs to it.
echo "[dev:docs] Starting Vocs docs app (apps/docs) at http://localhost:${DOCS_PORT}/docs..."
(
  cd "${DOCS_DIR}"
  if [[ ! -d node_modules ]]; then
    echo "[dev:docs] Installing docs dependencies in apps/docs..."
    npm install --silent --no-audit --fund=false
  fi
  npm run --silent dev -- --port "${DOCS_PORT}"
) &
docs_pid=$!
ensure_process_alive "${docs_pid}" "docs app" 3
wait_for_http "http://127.0.0.1:${DOCS_PORT}/docs" "docs" "${LAZU_DOCS_READY_TIMEOUT_SECONDS:-300}" "${docs_pid}"
echo "[dev:docs] Docs app is ready."

# Start frontend
echo "[dev:web] Starting Vite web app (apps/web) at http://localhost:${FRONTEND_PORT}..."
(
  cd "${FRONT_DIR}"
  export LAZU_DOCS_DEV_TARGET="http://127.0.0.1:${DOCS_PORT}"
  bun run dev -- --host 0.0.0.0 --port "${FRONTEND_PORT}" --strictPort
) &
frontend_pid=$!
ensure_process_alive "${frontend_pid}" "web app" 2
wait_for_http "http://127.0.0.1:${FRONTEND_PORT}/" "web app" "${LAZU_WEB_READY_TIMEOUT_SECONDS:-120}" "${frontend_pid}"

echo ""
echo "[dev:ready] Backend:         http://localhost:3000 (${db_label})"
echo "[dev:ready] Web app:         http://localhost:${FRONTEND_PORT}"
echo "[dev:ready] Docs:            http://localhost:${DOCS_PORT}/docs (also via the web app at /docs)"
echo "[dev:ready] ${db_label}:      ${db_host_port}"
echo "[dev:ready] Redis:           localhost:10001"
echo "[dev] Press Ctrl+C to stop"
echo ""

wait
