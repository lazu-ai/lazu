#!/usr/bin/env bash
# Schema-21 comparison. The caller provides old binaries and unchanged shared
# request tools; this runner never builds, migrates or connects to production.
set -euo pipefail
umask 077
export PYTHONDONTWRITEBYTECODE=1
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)

run_legacy_benchmark() {
  local out="$1"
  for tool in python3 docker; do command -v "$tool" >/dev/null || { echo "Missing $tool" >&2; exit 1; }; done
  DOCKER_HOST=$(python3 "$repo/ops/bench/gateway/environment.py" docker-endpoint)
  export DOCKER_HOST
  unset DOCKER_CONTEXT
  docker info >/dev/null
  # The unmodified old HTTP server binds :port, unlike the modern loopback
  # runner. Its experiment host must block public ingress to that port first.
  [[ "${LAZU_BENCH_LEGACY_BIND_ISOLATED:-}" == true ]] || {
    echo 'Legacy wildcard HTTP bind requires LAZU_BENCH_LEGACY_BIND_ISOLATED=true after host ingress isolation.' >&2
    exit 1
  }
  local legacy_bins="${LAZU_BENCH_LEGACY_BIN_DIR:-$repo/.local-results/gateway-bench/legacy21-bin}"
  local tool_bins="${LAZU_BENCH_TOOLS_BIN_DIR:-$repo/.local-results/gateway-bench/latest}"
  local pg_port=${LAZU_BENCH_PG_PORT:-15483}
  local redis_port=${LAZU_BENCH_REDIS_PORT:-16393}
  local upstream_port=${LAZU_BENCH_UPSTREAM_PORT:-10082}
  local gateway_port=${LAZU_BENCH_GATEWAY_PORT:-10110}
  local delay_port=${LAZU_BENCH_PG_DELAY_PORT:-15484}
  local pg_delay_ms=${LAZU_BENCH_PG_RTT_MS:-0}
  local duration=${LAZU_BENCH_DURATION:-8s}
  local pool=${LAZU_BENCH_LEGACY_SQL_POOL:-15}
  python3 - "$out" "$legacy_bins" "$tool_bins" "$pg_port" "$redis_port" "$upstream_port" "$gateway_port" "$delay_port" "$pg_delay_ms" "$pool" <<'PY'
import pathlib,socket,sys
out,legacy,tools=map(lambda value:pathlib.Path(value).resolve(),sys.argv[1:4])
for source in (legacy,tools):
    if source==out or source.is_relative_to(out):
        raise SystemExit('Input binary directory must be outside this run output')
sha=(legacy/'build-source.txt').read_text().strip()
if sha!='6a29da9e75ab76af38d270b9be58151c94c3e63d':
    raise SystemExit('Old binary build-source.txt must attest exact 6a29da9e7 source')
for directory,names in ((legacy,('gateway','migrate')),(tools,('mock','bench','pgdelay'))):
    for name in names:
        if not (directory/name).is_file(): raise SystemExit('Missing supplied benchmark binary: '+name)
ports=list(map(int,sys.argv[4:9]))
if len(set(ports))!=5: raise SystemExit('Fixture ports must be distinct')
for port in ports:
    if not 1024<=port<=65535: raise SystemExit('Fixture ports must be between 1024 and 65535')
    with socket.socket() as listener: listener.bind(('127.0.0.1',port))
if not 0<=int(sys.argv[9])<=1000: raise SystemExit('PostgreSQL RTT must be between 0 and 1000 ms')
if not 1<=int(sys.argv[10])<=20: raise SystemExit('Legacy pool must be between 1 and 20 (PostgreSQL max_connections=30)')
PY
  pool=$(python3 -c 'import sys; print(int(sys.argv[1]))' "$pool")
  mkdir -p "$out"
  python3 - "$out" <<'PY'
import pathlib,sys
out=pathlib.Path(sys.argv[1])
names=('results.json','results.md','stress.json','failed-direct.json','accounting.json','cold-warm.json',
       'sql-statements.json','legacy-observation.json','unsupported-metrics.json','measurement-error.json',
       'case-keys.json','keys.txt','single-key.txt','same-project-keys.txt','model-responses.jsonl',
       'migrate.log','gateway-seed.log','gateway.log','mock.log','pgdelay.log','environment-manifest.json',
       'binary-sha256.json','comparison-source-sha256.json','legacy-run-manifest.json','run-status.json')
for path in [*(out/name for name in names),*out.glob('samples-[0-9][0-9].jsonl')]:
    path.unlink(missing_ok=True)
PY
  for name in gateway migrate; do cp "$legacy_bins/$name" "$out/$name"; chmod 700 "$out/$name"; done
  for name in mock bench pgdelay; do cp "$tool_bins/$name" "$out/$name"; chmod 700 "$out/$name"; done
  cp "$legacy_bins/build-source.txt" "$out/legacy-build-source.txt"
  python3 - "$repo" "$out" "$pg_delay_ms" "$duration" "$pool" "${LAZU_BENCH_LEGACY_CASES:-}" "${LAZU_BENCH_STRESS_ENABLED:-false}" <<'PY'
import hashlib,json,pathlib,sys
repo,out=map(pathlib.Path,sys.argv[1:3])
paths=[*sorted((repo/'ops/bench/gateway/bench').glob('*.go')),
       repo/'ops/bench/gateway/mock/main.go',repo/'ops/bench/gateway/pgdelay/main.go',
       *(repo/'ops/bench/gateway'/name for name in ('legacy_setup.py','legacy_summary.py','legacy_run.sh','environment.py','evidence.py'))]
source={str(path.relative_to(repo)):hashlib.sha256(path.read_bytes()).hexdigest() for path in paths}
(out/'comparison-source-sha256.json').write_text(json.dumps(source,indent=2)+'\n')
(out/'binary-sha256.json').write_text(json.dumps({name:hashlib.sha256((out/name).read_bytes()).hexdigest()
    for name in ('gateway','migrate','mock','bench','pgdelay')},indent=2)+'\n')
manifest={'format':'lazu-gateway-legacy21-v1','legacy_source':(out/'legacy-build-source.txt').read_text().strip(),
    'source_attestation':'Caller supplied build-source.txt; runner records binary hashes and does not rebuild',
    'pg_rtt_ms':int(sys.argv[3]),'duration':sys.argv[4],'sql_max_open_conns':int(sys.argv[5]),
    'sql_max_idle_conns':int(sys.argv[5]),'postgres_max_connections':30,'gogc':50,'gomemlimit':'384MiB',
    'log_database':'same schema21 PostgreSQL database via empty LOG_SQL_DSN',
    'process_role':'api','relay_consumer':'original in-process consumer enabled',
    'model_log_enabled':True,'memory_cache_enabled':True,'pricing_snapshot_enabled':True,
    'cpu_admission_monitor_enabled':False,'capacity_scope':'Synthetic fixture configuration; not production capacity configuration',
    'matrix_selection':sys.argv[6] or 'shared full 12-case matrix',
    'stress_enabled':sys.argv[7].lower()=='true',
    'gateway_bind':'Legacy :port; caller attests public ingress blocked on this experiment host',
    'ttft':'Shared bench: first complete SSE content event containing synthetic text delta tok',
    'release_acceptance':None}
(out/'legacy-run-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
PY
  local fixture="lazu-gateway-legacy21-$$"
  local pg_container="$fixture-pg"
  local redis_container="$fixture-redis"
  # Traps refer to process globals so they remain valid through function exit.
  legacy_owned_pids=()
  legacy_owned_containers=()
  legacy_output="$out"
  cleanup_legacy() {
    local code=$?
    trap - EXIT INT TERM
    for pid in "${legacy_owned_pids[@]}"; do kill -CONT "$pid" 2>/dev/null || true; kill "$pid" 2>/dev/null || true; done
    for pid in "${legacy_owned_pids[@]}"; do wait "$pid" 2>/dev/null || true; done
    for container in "${legacy_owned_containers[@]}"; do docker rm -f "$container" >/dev/null 2>&1 || true; done
    python3 - "$legacy_output" "$code" <<'PY'
import json,pathlib,sys
out=pathlib.Path(sys.argv[1]);code=int(sys.argv[2])
(out/'run-status.json').write_text(json.dumps({'exit_code':code,'completed':code==0,
    'owned_fixture_cleanup_attempted':True,'release_acceptance':None},indent=2)+'\n')
PY
    exit "$code"
  }
  trap cleanup_legacy EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  legacy_owned_containers+=("$pg_container")
  docker run -d --name "$pg_container" -e POSTGRES_USER=lazu -e POSTGRES_PASSWORD=example-local-bench-password -e POSTGRES_DB=lazu -p "127.0.0.1:$pg_port:5432" postgres:16-alpine -c max_connections=30 -c shared_preload_libraries=pg_stat_statements -c pg_stat_statements.track=all >/dev/null
  legacy_owned_containers+=("$redis_container")
  docker run -d --name "$redis_container" -p "127.0.0.1:$redis_port:6379" redis:7-alpine redis-server --appendonly yes >/dev/null
  local ready=false
  for ((i=0;i<60;i++)); do
    if docker exec "$pg_container" psql -h 127.0.0.1 -U lazu -d lazu -Atc 'SELECT 1' >/dev/null 2>&1 && docker exec "$redis_container" redis-cli PING >/dev/null 2>&1; then ready=true; break; fi
    sleep 1
  done
  [[ "$ready" == true ]] || { echo 'Owned PostgreSQL/Redis fixture did not start' >&2; exit 1; }
  docker exec "$pg_container" psql -U lazu -d lazu -v ON_ERROR_STOP=1 -c 'CREATE EXTENSION pg_stat_statements;' >/dev/null
  python3 "$repo/ops/bench/gateway/environment.py" manifest --out "$out/environment-manifest.json" --pg "$pg_container" --redis "$redis_container"
  local root_password session_secret
  root_password=$(python3 -c 'import secrets; print(secrets.token_hex(24))')
  session_secret=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
  printf '{}\n' > "$out/config.yaml"
  local base_env=(env -i "PATH=$PATH" "APP_CONFIG_FILE=$out/config.yaml" "DATABASE_URL=postgres://lazu:example-local-bench-password@127.0.0.1:$pg_port/lazu?sslmode=disable" "LOG_SQL_DSN=" "REDIS_CONN_STRING=redis://127.0.0.1:$redis_port/0" "ROOT_USERNAME=benchroot" "ROOT_PASSWORD=$root_password" "SESSION_SECRET=$session_secret" "SQL_MAX_OPEN_CONNS=$pool" "SQL_MAX_IDLE_CONNS=$pool" "GOGC=50" "GOMEMLIMIT=384MiB" "MEMORY_CACHE_ENABLED=true" "PRICING_SNAPSHOT_ENABLED=true" "MODEL_LOG_ENABLED=true" "MODEL_LOG_PATH=$out/model-responses.jsonl" "DATA_EXPORT_ENABLED=false" "NODE_NAME=legacy21-bench" "PROCESS_ROLE=api" "RELAY_EVENT_CONSUMER_ENABLED=true")
  cd "$out"
  # The old migrator accepts no -command argument and has no ledger bootstrap.
  "${base_env[@]}" "$out/migrate" > "$out/migrate.log" 2>&1
  python3 - "$repo" "$pg_container" <<'PY'
import pathlib,subprocess,sys
sys.path.insert(0,str(pathlib.Path(sys.argv[1])/'ops/bench/gateway'))
from legacy_setup import validate_schema21
rows=subprocess.check_output(['docker','exec',sys.argv[2],'psql','-U','lazu','-d','lazu','-Atc',
    "SELECT scope||':'||version FROM schema_versions ORDER BY scope"],text=True).splitlines()
try:
    validate_schema21(rows)  # empty LOG_SQL_DSN: primary only, logs share it
except ValueError as error:
    raise SystemExit(str(error)) from None
PY
  "$out/mock" -addr "127.0.0.1:$upstream_port" > "$out/mock.log" 2>&1 & legacy_owned_pids+=("$!")
  "${base_env[@]}" LAZU_MIGRATION_MODE=validate "$out/gateway" -port "$gateway_port" -log-dir "$out/logs" > "$out/gateway-seed.log" 2>&1 & local seed_pid=$!; legacy_owned_pids+=("$seed_pid")
  wait_legacy_gateway() {
    python3 - "$gateway_port" "$1" <<'PY'
import os,sys,time,urllib.request
deadline=time.monotonic()+90
while time.monotonic()<deadline:
    try: os.kill(int(sys.argv[2]),0)
    except ProcessLookupError: raise SystemExit('Legacy gateway exited before readiness; inspect its log')
    try:
        with urllib.request.urlopen(f'http://127.0.0.1:{sys.argv[1]}/api/status',timeout=1) as response:
            if response.status==200: break
    except OSError: time.sleep(.2)
else: raise SystemExit('Legacy gateway readiness deadline expired')
PY
  }
  wait_legacy_gateway "$seed_pid"
  LAZU_BENCH_GATEWAY_URL="http://127.0.0.1:$gateway_port" LAZU_BENCH_UPSTREAM_URL="http://127.0.0.1:$upstream_port" LAZU_BENCH_PG_CONTAINER="$pg_container" LAZU_BENCH_REDIS_CONTAINER="$redis_container" ROOT_USERNAME=benchroot ROOT_PASSWORD="$root_password" python3 "$repo/ops/bench/gateway/legacy_setup.py" "$out/keys.txt"
  kill "$seed_pid"; wait "$seed_pid" || true
  # Remove the reaped seed PID so a later PID reuse cannot target another app.
  legacy_owned_pids=("${legacy_owned_pids[0]}")
  docker exec "$pg_container" psql -U lazu -d lazu -v ON_ERROR_STOP=1 -c "CREATE ROLE lazu_bench_legacy LOGIN PASSWORD 'example-local-bench-password'; GRANT ALL ON ALL TABLES IN SCHEMA public TO lazu_bench_legacy; GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO lazu_bench_legacy;" >/dev/null
  local effective_pg_port="$pg_port"
  if ((pg_delay_ms>0)); then
    "$out/pgdelay" -addr "127.0.0.1:$delay_port" -target "127.0.0.1:$pg_port" -rtt "${pg_delay_ms}ms" > "$out/pgdelay.log" 2>&1 & local delay_pid=$!; legacy_owned_pids+=("$delay_pid")
    python3 - "$delay_port" "$delay_pid" <<'PY'
import os,socket,sys,time
deadline=time.monotonic()+90
while time.monotonic()<deadline:
    try: os.kill(int(sys.argv[2]),0)
    except ProcessLookupError: raise SystemExit('PostgreSQL delay listener exited before readiness')
    try:
        with socket.create_connection(('127.0.0.1',int(sys.argv[1])),timeout=1): break
    except OSError: time.sleep(.1)
else: raise SystemExit('PostgreSQL delay listener deadline expired')
PY
    effective_pg_port="$delay_port"
  fi
  "${base_env[@]}" LAZU_MIGRATION_MODE=validate ROOT_SYNC_ON_START=false "DATABASE_URL=postgres://lazu_bench_legacy:example-local-bench-password@127.0.0.1:$effective_pg_port/lazu?sslmode=disable" "$out/gateway" -port "$gateway_port" -log-dir "$out/logs" > "$out/gateway.log" 2>&1 & local gateway_pid=$!; legacy_owned_pids+=("$gateway_pid")
  wait_legacy_gateway "$gateway_pid"
  LAZU_BENCH_PG_CONTAINER="$pg_container" LAZU_BENCH_REDIS_CONTAINER="$redis_container" LAZU_BENCH_GATEWAY_URL="http://127.0.0.1:$gateway_port" LAZU_BENCH_UPSTREAM_URL="http://127.0.0.1:$upstream_port" LAZU_BENCH_DURATION="$duration" LAZU_BENCH_STRESS_ENABLED="${LAZU_BENCH_STRESS_ENABLED:-false}" LAZU_BENCH_LEGACY_CASES="${LAZU_BENCH_LEGACY_CASES:-}" python3 "$repo/ops/bench/gateway/legacy_summary.py" "$out"
  cat "$out/results.md"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  exec python3 "$repo/ops/bench/gateway/environment.py" run-locked --repo "$repo" --out "${LAZU_BENCH_OUT_DIR:-.local-results/gateway-bench/legacy21-latest}" -- \
    "$BASH" -c 'source "$1"; run_legacy_benchmark "$LAZU_BENCH_LOCKED_OUT_DIR"' lazu-legacy-benchmark "$repo/ops/bench/gateway/legacy_run.sh"
fi
