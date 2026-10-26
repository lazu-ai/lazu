#!/usr/bin/env bash
set -euo pipefail
umask 077
export PYTHONDONTWRITEBYTECODE=1

repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
export GOCACHE="${GOCACHE:-$repo/.local-results/gateway-bench/.go-cache}"

run_benchmark() {
out="$1"
case "${LAZU_BENCH_MODE:-streaming}" in streaming|decisions) ;; *) echo 'Benchmark mode must be streaming or decisions' >&2; exit 1;; esac
for tool in go python3 docker; do command -v "$tool" >/dev/null || { echo "Missing $tool" >&2; exit 1; }; done
# Resolve local CLI metadata before touching Docker, then pin that Unix endpoint
# so a concurrent context switch cannot redirect later fixture operations.
DOCKER_HOST=$(python3 "$repo/ops/bench/gateway/environment.py" docker-endpoint)
export DOCKER_HOST
unset DOCKER_CONTEXT
docker info >/dev/null
mkdir -p "$out"
python3 - "$out" <<'PY'
import pathlib,sys
out=pathlib.Path(sys.argv[1])
# A failed run must never leave a previous run's PASS artifacts beside its
# partial measurements. Explicit RTT comparison snapshots are preserved.
names=('results.json','results.md','stress.json','failed-case.json','accounting.json',
       'settlement-visibility.json','writers-paused.json','sql-statements.json','model-responses.jsonl',
       'cold-warm.json','timing-profiles.json','timing-facts.json','case-keys.json','acceptance.json',
       'migrate.log','ledger-bootstrap.log','gateway-seed.log','gateway.log','worker.log','mock.log','pgdelay.log',
       'environment-manifest.json')
for path in [*(out/name for name in names),*out.glob('samples-[0-9][0-9].jsonl')]:
    path.unlink(missing_ok=True)
PY
pg_port=${LAZU_BENCH_PG_PORT:-15473}
redis_port=${LAZU_BENCH_REDIS_PORT:-16383}
upstream_port=${LAZU_BENCH_UPSTREAM_PORT:-10062}
gateway_port=${LAZU_BENCH_GATEWAY_PORT:-10070}
delay_port=${LAZU_BENCH_PG_DELAY_PORT:-15474}
pg_delay_ms=${LAZU_BENCH_PG_RTT_MS:-0}
duration=${LAZU_BENCH_DURATION:-8s}
python3 - "$pg_port" "$redis_port" "$upstream_port" "$gateway_port" "$delay_port" <<'PY'
import socket,sys
for raw in sys.argv[1:]:
    port=int(raw)
    if not 1024 <= port <= 65535: raise SystemExit('Fixture ports must be between 1024 and 65535')
    s=socket.socket(); s.bind(('127.0.0.1',port)); s.close()
if len(set(sys.argv[1:]))!=5: raise SystemExit('Fixture ports must be distinct')
PY
python3 - "$pg_delay_ms" <<'PY'
import sys
if not 0 <= int(sys.argv[1]) <= 1000: raise SystemExit('PostgreSQL RTT must be between 0 and 1000 ms')
PY
fixture="lazu-gateway-bench-$$"
spool_dir="$out/spool-$$-$(python3 -c 'import secrets; print(secrets.token_hex(4))')"
pg_container="$fixture-pg"
redis_container="$fixture-redis"
pids=()
containers=()
cleanup() {
  for pid in "${pids[@]}"; do kill -CONT "$pid" 2>/dev/null || true; kill "$pid" 2>/dev/null || true; done
  for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  for container in "${containers[@]}"; do docker rm -f "$container" >/dev/null 2>&1 || true; done
}
trap cleanup EXIT INT TERM

cd "$repo"
go build -o "$out/gateway" ./apps/api
go build -o "$out/worker" ./apps/worker
go build -o "$out/migrate" ./apps/migrate
go build -o "$out/mock" ./ops/bench/gateway/mock
go build -o "$out/bench" ./ops/bench/gateway/bench
go build -o "$out/pgdelay" ./ops/bench/gateway/pgdelay
git rev-parse HEAD > "$out/checkout-sha.txt"
git diff --stat > "$out/checkout-diff.txt"
git status --short > "$out/checkout-status.txt"
python3 - "$repo" "$out" <<'PY'
import hashlib,json,pathlib,subprocess,sys
repo,out=map(pathlib.Path,sys.argv[1:])
paths=subprocess.check_output(['git','ls-files','-co','--exclude-standard','-z'],cwd=repo).decode().split('\0')
source={}
for name in sorted(set(paths)):
    path=repo/name
    runtime_root=name.startswith(('internal/','apps/api/','apps/worker/','apps/migrate/','config/backend/','packages/'))
    if path.is_file() and not name.endswith('_test.go') and (name in ('go.mod','go.sum') or name.endswith('.go') or runtime_root or name.startswith('ops/bench/gateway/') and path.suffix in ('.sh','.py')):
        source[name]=hashlib.sha256(path.read_bytes()).hexdigest()
(out/'runtime-source-sha256.json').write_text(json.dumps(source,indent=2)+'\n')
(out/'binary-sha256.json').write_text(json.dumps({name:hashlib.sha256((out/name).read_bytes()).hexdigest() for name in ('gateway','worker','migrate','mock','bench','pgdelay')},indent=2)+'\n')
PY
docker run -d --name "$pg_container" -e POSTGRES_USER=lazu -e POSTGRES_PASSWORD=example-local-bench-password -e POSTGRES_DB=lazu -p "127.0.0.1:$pg_port:5432" postgres:16-alpine -c max_connections=30 -c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track=all -c track_commit_timestamp=on >/dev/null
containers+=("$pg_container")
docker run -d --name "$redis_container" -p "127.0.0.1:$redis_port:6379" redis:7-alpine redis-server --appendonly yes >/dev/null
containers+=("$redis_container")
for ((i=0;i<60;i++)); do docker exec "$pg_container" psql -h 127.0.0.1 -U lazu -d lazu -Atc 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
docker exec "$pg_container" psql -U lazu -v ON_ERROR_STOP=1 -c 'CREATE EXTENSION IF NOT EXISTS pg_stat_statements;'
docker exec "$pg_container" psql -U lazu -v ON_ERROR_STOP=1 -c 'CREATE DATABASE telemetry;' >/dev/null
python3 "$repo/ops/bench/gateway/environment.py" manifest --out "$out/environment-manifest.json" --pg "$pg_container" --redis "$redis_container"

root_password=$(python3 -c 'import secrets; print(secrets.token_hex(24))')
session_secret=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
metrics_token=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
printf '{}\n' > "$out/config.yaml"
base_env=(env -i "PATH=$PATH" "GOCACHE=$GOCACHE" "APP_CONFIG_FILE=$out/config.yaml" "DATABASE_URL=postgres://lazu:example-local-bench-password@127.0.0.1:$pg_port/lazu?sslmode=disable" "TELEMETRY_DATABASE_URL=postgres://lazu:example-local-bench-password@127.0.0.1:$pg_port/telemetry?sslmode=disable" "REDIS_CONN_STRING=redis://127.0.0.1:$redis_port/0" "ROOT_USERNAME=benchroot" "ROOT_PASSWORD=$root_password" "SESSION_SECRET=$session_secret" "SQL_MAX_OPEN_CONNS=6" "SQL_MAX_IDLE_CONNS=6" "LOG_SQL_MAX_OPEN_CONNS=6" "LOG_SQL_MAX_IDLE_CONNS=6" "GOGC=50" "GOMEMLIMIT=384MiB" "MEMORY_CACHE_ENABLED=true" "PRICING_SNAPSHOT_ENABLED=true" "MODEL_LOG_ENABLED=true" "MODEL_LOG_PATH=$out/model-responses.jsonl" "LEDGER_SPOOL_DIR=$out/spool" "DATA_EXPORT_ENABLED=false" "NODE_NAME=local-bench" "LAZU_BIND_HOST=127.0.0.1" "SHUTDOWN_DRAIN_DELAY_SECONDS=0")
# Redis pool size under test (experiments); default is the gateway's own.
if [ -n "${LAZU_BENCH_REDIS_POOL_SIZE:-}" ]; then base_env+=("REDIS_POOL_SIZE=$LAZU_BENCH_REDIS_POOL_SIZE"); fi
cd "$out"
base_env+=("HOT_PATH_METRICS_ENABLED=true" "METRICS_ENABLED=true" "METRICS_TOKEN=$metrics_token" "LEDGER_SPOOL_DIR=$spool_dir")
"${base_env[@]}" "$out/migrate" -command schema > "$out/migrate.log" 2>&1
"${base_env[@]}" "LAZU_MIGRATION_SOURCE_URL=postgres://lazu:example-local-bench-password@127.0.0.1:$pg_port/lazu?sslmode=disable" "$out/migrate" -command ledger-bootstrap > "$out/ledger-bootstrap.log" 2>&1
"$out/mock" -addr "127.0.0.1:$upstream_port" > "$out/mock.log" 2>&1 & pids+=("$!")
"${base_env[@]}" PROCESS_ROLE=all-in-one RELAY_EVENT_CONSUMER_ENABLED=false "$out/gateway" -port "$gateway_port" -log-dir "$out/logs" > "$out/gateway-seed.log" 2>&1 & seed_pid=$!; pids+=("$seed_pid")
python3 - "$gateway_port" "$seed_pid" <<'PY'
import sys,time,urllib.request,os
url=f'http://127.0.0.1:{sys.argv[1]}/api/status'
for _ in range(90):
    try: os.kill(int(sys.argv[2]),0)
    except ProcessLookupError: raise SystemExit("Gateway startup exited; inspect gateway-seed.log")
    try:
        with urllib.request.urlopen(url,timeout=1) as r:
            if r.status==200: break
    except Exception: time.sleep(1)
else: raise SystemExit('Gateway startup failed; inspect gateway-seed.log')
PY
LAZU_BENCH_GATEWAY_URL="http://127.0.0.1:$gateway_port" LAZU_BENCH_UPSTREAM_URL="http://127.0.0.1:$upstream_port" LAZU_BENCH_PG_CONTAINER="$pg_container" ROOT_USERNAME=benchroot ROOT_PASSWORD="$root_password" python3 "$repo/ops/bench/gateway/setup.py" "$out/keys.txt"
kill "$seed_pid"; wait "$seed_pid" || true
# Separate SQL roles let pg_stat_statements attribute API/consumer I/O exactly.
for database in lazu telemetry; do
  if [[ "$database" == lazu ]]; then
    docker exec "$pg_container" psql -U lazu -d "$database" -v ON_ERROR_STOP=1 -c "CREATE ROLE lazu_bench_api LOGIN PASSWORD 'example-local-bench-password'; CREATE ROLE lazu_bench_worker LOGIN PASSWORD 'example-local-bench-password';" >/dev/null
  fi
  docker exec "$pg_container" psql -U lazu -d "$database" -v ON_ERROR_STOP=1 -c 'GRANT ALL ON ALL TABLES IN SCHEMA public TO lazu_bench_api,lazu_bench_worker; GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO lazu_bench_api,lazu_bench_worker;' >/dev/null
done
effective_pg_port="$pg_port"
if ((pg_delay_ms > 0)); then
  "$out/pgdelay" -addr "127.0.0.1:$delay_port" -target "127.0.0.1:$pg_port" -rtt "${pg_delay_ms}ms" > "$out/pgdelay.log" 2>&1 & pids+=("$!")
  python3 - "$delay_port" "$!" <<'PY'
import os,socket,sys,time
def wait_for_ready(probe,alive,timeout=90,interval=.1):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        if not alive(): raise RuntimeError('Owned fixture exited before readiness')
        if probe(): return
        time.sleep(interval)
    raise TimeoutError('Owned fixture startup deadline expired')
def alive():
    try: os.kill(int(sys.argv[2]),0); return True
    except ProcessLookupError: return False
def listening():
    try:
        with socket.create_connection(('127.0.0.1',int(sys.argv[1])),timeout=1): return True
    except OSError: return False
wait_for_ready(listening,alive)
PY
  effective_pg_port="$delay_port"
fi
"${base_env[@]}" PROCESS_ROLE=api RELAY_EVENT_CONSUMER_ENABLED=false "DATABASE_URL=postgres://lazu_bench_api:example-local-bench-password@127.0.0.1:$effective_pg_port/lazu?sslmode=disable" "TELEMETRY_DATABASE_URL=postgres://lazu_bench_api:example-local-bench-password@127.0.0.1:$effective_pg_port/telemetry?sslmode=disable" "$out/gateway" -port "$gateway_port" -log-dir "$out/logs" > "$out/gateway.log" 2>&1 & gateway_pid=$!; pids+=("$gateway_pid")
"${base_env[@]}" PROCESS_ROLE=worker LAZU_DB_POOL_ROLE=worker LAZU_WORKER_TASKS=control_plane_snapshot,settlement_outbox_reconciler "DATABASE_URL=postgres://lazu_bench_worker:example-local-bench-password@127.0.0.1:$effective_pg_port/lazu?sslmode=disable" "TELEMETRY_DATABASE_URL=postgres://lazu_bench_worker:example-local-bench-password@127.0.0.1:$effective_pg_port/telemetry?sslmode=disable" "$out/worker" > "$out/worker.log" 2>&1 & worker_pid=$!; pids+=("$worker_pid")
if ((pg_delay_ms > 0)); then
python3 - "$gateway_port" "$gateway_pid" "$worker_pid" <<'PY'
import os,sys,time,urllib.request
def wait_for_ready(probe,alive,timeout=90,interval=.1):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        if not alive(): raise RuntimeError('Owned fixture exited before readiness')
        if probe(): return
        time.sleep(interval)
    raise TimeoutError('Owned fixture startup deadline expired')
def alive():
    try:
        for pid in sys.argv[2:4]: os.kill(int(pid),0)
        return True
    except ProcessLookupError: return False
def ready():
    try:
        with urllib.request.urlopen(f'http://127.0.0.1:{sys.argv[1]}/readyz',timeout=1) as r:
            return r.status==200
    except OSError: return False
wait_for_ready(ready,alive)
PY
else
  sleep 3
fi
LAZU_BENCH_PG_CONTAINER="$pg_container" LAZU_BENCH_REDIS_CONTAINER="$redis_container" LAZU_BENCH_GATEWAY_URL="http://127.0.0.1:$gateway_port" LAZU_BENCH_UPSTREAM_URL="http://127.0.0.1:$upstream_port" LAZU_BENCH_DURATION="$duration" LAZU_BENCH_STRESS_ENABLED="${LAZU_BENCH_STRESS_ENABLED:-true}" LAZU_BENCH_METRICS_TOKEN="$metrics_token" LAZU_BENCH_SPOOL_DIR="$spool_dir" LAZU_BENCH_WORKER_PID="$worker_pid" LAZU_BENCH_PG_RTT_MS="$pg_delay_ms" python3 "$repo/ops/bench/gateway/summary.py" "$out"
cat "$out/results.md"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  exec python3 "$repo/ops/bench/gateway/environment.py" run-locked --repo "$repo" --out "${LAZU_BENCH_OUT_DIR:-}" -- \
    "$BASH" -c 'source "$1"; run_benchmark "$LAZU_BENCH_LOCKED_OUT_DIR"' lazu-benchmark "$repo/ops/bench/gateway/run.sh"
fi
