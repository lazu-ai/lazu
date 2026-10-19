"""Only synthetic, loopback fixtures; machine results and a reviewable table."""
import json, os, pathlib, re, signal, subprocess, sys, time, urllib.request
from evidence import dispatch_counters, require_sample_coverage, clock_offset_bounds, commit_latency_upper_ms, accounting_checks

out = pathlib.Path(sys.argv[1])
pg = os.environ['LAZU_BENCH_PG_CONTAINER']
redis = os.environ['LAZU_BENCH_REDIS_CONTAINER']
gateway = os.environ['LAZU_BENCH_GATEWAY_URL']
upstream = os.environ['LAZU_BENCH_UPSTREAM_URL']
duration = os.environ['LAZU_BENCH_DURATION']
decision_mode = os.environ.get('LAZU_BENCH_MODE', 'streaming') == 'decisions'
warm_api = 'decisions-openai' if decision_mode else 'responses'
keys = (out / 'keys.txt').read_text().strip()
sample_files = []

def sql(query, database='lazu'):
    return subprocess.check_output(['docker', 'exec', pg, 'psql', '-U', 'lazu', '-d', database, '-Atc', query], text=True).strip()

def counters(allow_unseen_required=False):
    rows = sql("SELECT r.rolname, sum(s.calls)::bigint, sum(CASE WHEN s.query ~* '^(BEGIN|START TRANSACTION)' THEN s.calls ELSE 0 END)::bigint FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname IN ('lazu_bench_api','lazu_bench_worker') GROUP BY r.rolname")
    values = {'api_sql': 0, 'worker_sql': 0, 'transactions': 0}
    for row in rows.splitlines():
        role, calls, transactions = row.split('|')
        values['api_sql' if role.endswith('api') else 'worker_sql'] += int(calls)
        values['transactions'] += int(transactions)
    raw = subprocess.check_output(['docker', 'exec', redis, 'redis-cli', 'INFO', 'commandstats'], text=True)
    values['redis'] = sum(int(line.split('calls=')[1].split(',')[0]) for line in raw.splitlines() if line.startswith('cmdstat_'))
    request = urllib.request.Request(gateway + '/metrics', headers={'Authorization': 'Bearer ' + os.environ['LAZU_BENCH_METRICS_TOKEN']})
    with urllib.request.urlopen(request) as response:
        metrics = response.read().decode().splitlines()
    values.update(dispatch_counters(metrics, allow_unseen_required))
    return values

clock_samples = []
def calibrate_clock(phase):
    for _ in range(3):
        before = time.time_ns() // 1_000_000
        postgres_ms = int(sql('SELECT (extract(epoch FROM clock_timestamp())*1000)::bigint'))
        after = time.time_ns() // 1_000_000
        clock_samples.append({'phase': phase, 'host_before_ms': before, 'postgres_ms': postgres_ms, 'host_after_ms': after})
    clock_offset_bounds(clock_samples)

calibrate_clock('before measured requests')

def stats():
    with urllib.request.urlopen(upstream + '/stats') as response:
        return dict((key, int(value)) for key, value in (part.split('=') for part in response.read().decode().split()))

def api_sql_shapes():
    rows = json.loads(sql("SELECT COALESCE(json_agg(rows)::text,'[]') FROM (SELECT s.dbid::text || ':' || s.queryid::text AS id,s.calls,s.query FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname='lazu_bench_api') rows"))
    return {row['id']: row for row in rows}

def api_sql_delta(before):
    result = []
    for identity, row in api_sql_shapes().items():
        calls = row['calls'] - before.get(identity, {}).get('calls', 0)
        if calls > 0:
            query = row['query'].lower()
            category = 'identity_refresh' if 'credential_expires_at' in query and 'actor_needs_password' in query else 'identity_budget_or_cold_admission' if 'select' in query and '"spend_budgets"' in query else 'other'
            result.append({'calls':calls,'category':category,'query':row['query']})
    return sorted(result, key=lambda row: -row['calls'])

def bench(api, kb, workers, target, warm=False, fixture_keys=None, rps=0, case_duration=None):
    endpoint = '/v1/responses' if api == 'responses' else '/v1/decisions' if api.startswith('decisions-') else '/v1/chat/completions'
    if api == 'decisions-legacy' and target == 'direct':
        endpoint = '/v1/systemone'
    cmd = [str(out / 'bench'), '-api', api, '-kb', str(kb), '-c', str(workers), '-d', case_duration or ('2s' if warm else duration), '-rps', str(rps), '-url', (gateway if target == 'gateway' else upstream) + endpoint, '-keys', (fixture_keys or keys) if target == 'gateway' else 'mock', '-json', '-label', f'{api}-{kb}KB-c{workers}-{target}']
    if api.startswith('decisions-'):
        model = 'gpt-6-luna' if api == 'decisions-openai' else 'jev-1.13.0' if target == 'direct' else 'jev-1.13'
        cmd += ['-stream=false', '-model', model]
    if target == 'gateway' and not warm:
        sample = out / f'samples-{len(sample_files):02d}.jsonl'
        sample_files.append(sample)
        cmd += ['-samples', str(sample)]
    return json.loads(subprocess.check_output(cmd, text=True))

warm_results = []
def warm(api, kb, workers, fixture_keys=None, case_duration=None, phase='warm'):
    before = counters(allow_unseen_required=not warm_results)
    result = bench(api, kb, workers, 'gateway', warm=True, fixture_keys=fixture_keys, case_duration=case_duration)
    after = counters()
    warm_results.append({'phase': phase, 'gateway': result,
                         'counter_before': before, 'counter_after': after,
                         **{key: after[key]-before[key] if before[key] is not None else None for key in before}})
    (out / 'cold-warm.json').write_text(json.dumps(warm_results, indent=2))
    if result['requests'] == 0 or result['status_codes'].get('200', 0) != result['requests']:
        raise SystemExit(f"{phase} failed: {result['status_codes']} {result.get('error_samples')}; inspect cold-warm.json")

# Initialize distinct payers serially: cold budget-fence contention must not
# disappear in a discarded parallel warm-up. Keep every cold result/counter.
for index, key in enumerate(keys.split(',')):
    warm(warm_api, 1, 1, fixture_keys=key, case_duration='200ms', phase=f'cold-payer-{index}')
case_keys = json.loads((out / 'case-keys.json').read_text())
results = []
for api in (('decisions-legacy', 'decisions-openai') if decision_mode else ('responses', 'chat')):
    for kb, workers in (((1,1), (1,8), (1,32)) if decision_mode else ((1,1), (100,1), (600,1), (600,8), (1,32), (1,50))):
        direct = bench(api,kb,workers,'direct')
        if direct['requests'] == 0 or direct['status_codes'].get('200',0) != direct['requests']:
            (out/'failed-case.json').write_text(json.dumps(direct,indent=2))
            raise SystemExit('Direct baseline has failed/incomplete responses; inspect failed-case.json')
        fixture_keys = ','.join(case_keys[len(results)])
        warm(api,kb,max(workers,10),fixture_keys=fixture_keys,phase=f'{api}-{kb}KB-c{workers}')
        stats()
        shapes_before = api_sql_shapes()
        before = counters()
        measured = bench(api,kb,workers,'gateway',fixture_keys=fixture_keys)
        after = counters()
        connections = stats()
        successes = measured['status_codes'].get('200',0)
        if successes != measured['requests'] or successes == 0:
            (out / 'failed-case.json').write_text(json.dumps(measured, indent=2))
            raise SystemExit(f"Fixture request failed: {api} {kb}KB c{workers} {measured['status_codes']} {measured.get('error_samples')}; inspect gateway.log")
        row = {'api': api, 'kilobytes': kb, 'concurrency': workers, 'direct':direct, 'gateway':measured,
               'api_sql_statement_count': after['api_sql']-before['api_sql'],
               'worker_sql_statement_count': after['worker_sql']-before['worker_sql'],
               'api_sql_shapes': api_sql_delta(shapes_before),
               'ttft_overhead_p50_ms': measured['ttft_p50_ms']-direct['ttft_p50_ms'],
               'ttft_overhead_p95_ms': measured['ttft_p95_ms']-direct['ttft_p95_ms'],
               'new_connection_ratio': connections['new_conns']/max(1,connections['requests']),
               **{key+'_per_request':(after[key]-before[key])/successes for key in before}}
        results.append(row)
        (out/'results.json').write_text(json.dumps(results,indent=2))
        print(f"Measured {api} {kb}KB c{workers}: n={successes}",flush=True)

stress_results = []
if not decision_mode and os.environ.get('LAZU_BENCH_STRESS_ENABLED', 'true').lower() == 'true':
    for scenario, filename in (('single key', 'single-key.txt'), ('same project / ten keys', 'same-project-keys.txt')):
        fixture_keys = (out / filename).read_text().strip()
        warm('responses', 1, 10, fixture_keys=fixture_keys, phase=f'stress-{scenario}')
        shapes_before = api_sql_shapes()
        before = counters()
        measured = bench('responses', 1, 750, 'gateway', fixture_keys=fixture_keys, rps=500, case_duration='60s')
        after = counters()
        successes = measured['status_codes'].get('200', 0)
        if successes != measured['requests'] or successes == 0:
            (out / 'failed-case.json').write_text(json.dumps(measured, indent=2))
            raise SystemExit(f"Stress fixture failed: {scenario} {measured['status_codes']} {measured.get('error_samples')}; inspect gateway.log")
        row = {'scenario': scenario, 'gateway': measured,
               'api_sql_statement_count':after['api_sql']-before['api_sql'],
               'worker_sql_statement_count':after['worker_sql']-before['worker_sql'],
               'api_sql_shapes':api_sql_delta(shapes_before),
               **{key+'_per_request':(after[key]-before[key])/successes for key in before}}
        stress_results.append(row)
        (out / 'stress.json').write_text(json.dumps(stress_results, indent=2))
        print(f"Measured 500 RPS / 60 seconds / {scenario}: n={successes}", flush=True)

# Both independent writers live in this one owned jobs process. Pausing it
# leaves all admission/settlement Lua operations running and makes any API SQL
# unambiguous. Always resume, including exceptions and Ctrl-C.
# The six-case Decisions mode leaves this cohort unused. Give paused-writer
# requests a full ordinary identity TTL, rather than reusing the initial keys
# whose cache entries may expire during the measurement after the matrix.
paused_keys = ','.join(case_keys[len(results)]) if decision_mode else None
warm(warm_api, 1, 10, fixture_keys=paused_keys, phase='writers-paused')
worker_pid = int(os.environ['LAZU_BENCH_WORKER_PID'])
os.kill(worker_pid, signal.SIGSTOP)
try:
    # Let any statement already sent before SIGSTOP finish before taking the
    # baseline. Both consumers remain stopped throughout measured requests.
    time.sleep(max(.2, int(os.environ.get('LAZU_BENCH_PG_RTT_MS', '0')) / 500))
    paused_shapes_before = api_sql_shapes()
    before = counters()
    measured = bench(warm_api, 1, 8, 'gateway', fixture_keys=paused_keys)
    after = counters()
    successes = measured['status_codes'].get('200', 0)
    paused = {'gateway': measured, 'api_sql_statements': after['api_sql']-before['api_sql'],
              'worker_sql_statements': after['worker_sql']-before['worker_sql'],
              'api_sql_shapes': api_sql_delta(paused_shapes_before),
              'redis_required_per_request': (after['redis_request_dispatches']-before['redis_request_dispatches'])/max(1,successes),
              'passed': successes == measured['requests'] and successes > 0 and after['api_sql'] == before['api_sql'] and after['worker_sql'] == before['worker_sql']}
    (out / 'writers-paused.json').write_text(json.dumps(paused, indent=2))
finally:
    os.kill(worker_pid, signal.SIGCONT)

def redis_value(*args):
    raw = subprocess.check_output(['docker', 'exec', redis, 'redis-cli', '--json', *args], text=True)
    return json.loads(raw)

def stream_status(stream, group):
    groups = redis_value('XINFO', 'GROUPS', stream)
    for candidate in groups:
        row = candidate if isinstance(candidate, dict) else dict(zip(candidate[::2], candidate[1::2]))
        if row['name'] == group:
            return {'pending': int(row['pending']), 'lag': row['lag']}
    raise RuntimeError(f'Missing consumer group for {stream}')

def drain_status():
    financial = stream_status('lazu:ledger:events', 'ledger-writers')
    telemetry = stream_status('lazu:relay_events', 'lazu-relay-event-workers')
    return {'financial_pending': financial['pending'], 'financial_lag': financial['lag'],
            'telemetry_pending': telemetry['pending'], 'telemetry_lag': telemetry['lag'],
            'uncommitted': redis_value('ZCARD', 'lazu:ledger:uncommitted'),
            'exposure': int(redis_value('GET', 'lazu:ledger:exposure') or 0),
            'reserved_requests': redis_value('ZCARD', 'lazu:ledger:expiry'),
            'financial_dead': redis_value('XLEN', 'lazu:ledger:dead'),
            'telemetry_dead': redis_value('XLEN', 'lazu:relay_events:dead'),
            'spool_files': len(list(pathlib.Path(os.environ['LAZU_BENCH_SPOOL_DIR']).glob('*.json')))}

for _ in range(60):
    drained = drain_status()
    if all(value == 0 for value in drained.values()):
        break
    time.sleep(1)
else:
    print(f'Accounting failed to drain: {drained}; extracting remaining evidence before exit', flush=True)

wallets = []
for row in sql("SELECT u.id,u.quota,COALESCE(sum(w.delta_microusd),0)::bigint FROM users u LEFT JOIN wallet_ledgers w ON w.user_id=u.id WHERE u.username ~ '^perf[0-9]+$' GROUP BY u.id,u.quota ORDER BY u.id").splitlines():
    uid, balance, ledger_sum = map(int, row.split('|'))
    redis_balance = int(redis_value('HGET', f'lazu:ledger:wallet:{uid}', 'avail'))
    wallets.append({'user_id': uid, 'sql_balance': balance, 'redis_balance': redis_balance,
                    'wallet_ledger_sum': ledger_sum, 'equal': balance == redis_balance == ledger_sum})
operations = int(sql('SELECT count(*) FROM redis_ledger_operations'))
ledger_entries = int(sql("SELECT count(*) FROM wallet_ledgers WHERE ref_type IN ('relay_redis_settle','relay_redis_refund')"))
accounting = {'drain': drained, 'wallets': wallets, 'operations': operations, 'financial_ledger_entries': ledger_entries,
              **accounting_checks(drained, wallets, operations, ledger_entries)}
(out / 'accounting.json').write_text(json.dumps(accounting, indent=2))

commit_times = {}
calibrate_clock('after measured requests')
clock_bounds = clock_offset_bounds(clock_samples)
for row in sql("SELECT request_id,(extract(epoch FROM pg_xact_commit_timestamp(xmin))*1000)::bigint FROM redis_ledger_operations WHERE kind='settle'").splitlines():
    request_id, committed = row.split('|')
    if committed:
        commit_times[request_id] = int(committed)
lags = []
missing_commits = 0
measured_phases = [*(row['gateway'] for row in results), *(row['gateway'] for row in stress_results), paused['gateway']]
for filename, measured_phase in zip(sample_files, measured_phases):
    samples = [json.loads(line) for line in filename.read_text().splitlines()]
    require_sample_coverage(samples, measured_phase['status_codes'].get('200', 0))
    # Paused-writer requests intentionally test backlog and have a different
    # visibility expectation; exclude that final case from the P99 sample.
    if filename == sample_files[-1]:
        continue
    for sample in samples:
        committed = commit_times.get(sample['request_id'])
        if committed is None:
            missing_commits += 1
        else:
            lags.append(commit_latency_upper_ms(committed, sample['eof_ms'], clock_bounds))
lags.sort()
rtt_ms = int(os.environ.get('LAZU_BENCH_PG_RTT_MS', '0'))
visibility = {'samples': len(lags), 'missing_commits': missing_commits, 'pg_added_rtt_ms': rtt_ms,
              'clock_alignment':clock_bounds,'latency_basis':'conservative upper bound using PostgreSQL-minus-host lower clock offset',
              'p99_commit_after_eof_ms': lags[int(.99*(len(lags)-1))] if lags else None,
              'max_commit_after_eof_ms': max(lags) if lags else None,
              'limit_ms': 3000 if rtt_ms else 2000}
visibility['passed'] = bool(lags) and not missing_commits and visibility['p99_commit_after_eof_ms'] <= visibility['limit_ms']
(out / 'settlement-visibility.json').write_text(json.dumps(visibility, indent=2))
# Preserve normalized SQL shapes and role attribution, without literal values.
sql_shapes = json.loads(sql("SELECT COALESCE(json_agg(rows)::text,'[]') FROM (SELECT r.rolname AS role,s.calls,s.query FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname IN ('lazu_bench_api','lazu_bench_worker') ORDER BY s.calls DESC LIMIT 80) rows"))
(out / 'sql-statements.json').write_text(json.dumps(sql_shapes, indent=2))

# The trace distinguishes actual gateway preparation from provider-first-byte
# variation. Sample only the normal matrix, whose credential TTL is fresh.
profiles = []
timing_facts = []
phase_names = ('gateway_pre_ms','upstream_ttfb_ms','gateway_post_ms',
               'gateway_billing_ms','gateway_consume_log_ms','gateway_settle_encode_ms',
               'gateway_settle_ms','gateway_settle_eval_ms','gateway_capture_ms','gateway_routing_finish_ms','gateway_middleware_finish_ms')
for row, filename in zip(results, sample_files):
    samples = [json.loads(line) for line in filename.read_text().splitlines()]
    ids = [s['request_id'] for s in samples]
    if not all(re.fullmatch(r'[A-Za-z0-9_-]{1,100}', value) for value in ids):
        raise SystemExit('Unexpected request ID format in trace sample')
    clauses = ','.join("'"+value+"'" for value in ids)
    selections = ','.join(f"other::jsonb->'{name}' AS {name}" for name in phase_names)
    fields = sql(f"SELECT COALESCE(json_agg(rows)::text,'[]') FROM (SELECT request_id,{selections} FROM logs WHERE request_ts >= {min(s['eof_ms'] for s in samples)-120000} AND request_ts <= {max(s['eof_ms'] for s in samples)+1000} AND request_id IN ({clauses})) rows", 'telemetry')
    facts = json.loads(fields)
    profile = {'api':row['api'],'kilobytes':row['kilobytes'],'concurrency':row['concurrency'],'requests':len(ids),'timing_rows':len(facts)}
    timing_facts.append({'api':row['api'],'kilobytes':row['kilobytes'],'concurrency':row['concurrency'],'facts':facts})
    for name in phase_names:
        values = sorted(f[name] for f in facts if f.get(name) is not None)
        profile[name] = {'samples':len(values),'p50':values[len(values)//2] if values else None,'p95':values[int(.95*(len(values)-1))] if values else None,'max':max(values) if values else None}
    profiles.append(profile)
(out / 'timing-profiles.json').write_text(json.dumps(profiles, indent=2))
(out / 'timing-facts.json').write_text(json.dumps(timing_facts, indent=2))

latency_cases = [row for row in results if row['concurrency'] == 1 or row['kilobytes'] == 600]
limits = {1:5,100:15,600:50}
checks = {
    'short_warm_api_zero_sql':all(row['api_sql_statement_count'] == 0 for row in results),
    'short_warm_redis_at_most_three':all(0 < row['redis_request_dispatches_per_request'] <= 3 for row in results),
    'ttft_p50_body_targets':all(row['ttft_overhead_p50_ms'] <= limits[row['kilobytes']] for row in latency_cases),
    'single_request_tail_at_most_fifteen_ms':all(row['gateway']['tail_max_ms'] <= 15 for row in results if row['concurrency'] == 1),
    'fifty_concurrent_tail_p95_at_most_fifty_ms':all(row['gateway']['tail_p95_ms'] <= 50 for row in results if row['concurrency'] == 50),
    'fresh_connection_ratio_below_one_percent':all(row['new_connection_ratio'] < .01 for row in results if row['concurrency'] >= 32),
    'both_stress_cases_offer_five_hundred_rps_for_sixty_seconds':len(stress_results) == 2 and all(row['gateway']['offered_rps'] == 500 and row['gateway']['duration_seconds'] == 60 and row['gateway']['requests_per_second'] >= 495 for row in stress_results),
    'writers_paused':paused['passed'],
    'settlement_visibility':visibility['passed'],
    'accounting':accounting['passed'],
    'timing_fact_coverage':all(row['requests'] == row['timing_rows'] and all(row[name]['samples'] == row['requests'] for name in ('gateway_pre_ms','upstream_ttfb_ms','gateway_post_ms')) for row in profiles),
}
if globals().get('decision_mode', False):
    # Decisions has separate channel-capacity admission and no SSE TTFT. Keep
    # its measured dispatch counts separate from the unchanged streaming gates.
    checks = {
        'six_decision_cases': len(results) == 6,
        'short_warm_api_zero_sql': all(row['api_sql_statement_count'] == 0 for row in results),
        'redis_dispatches_measured': all(row['redis_request_dispatches_per_request'] > 0 and row['redis_api_dispatches_per_request'] >= row['redis_request_dispatches_per_request'] for row in results),
        'writers_paused': paused['passed'],
        'settlement_visibility': visibility['passed'],
        'accounting': accounting['passed'],
    }
(out / 'acceptance.json').write_text(json.dumps({'checks':checks,'all_numeric_checks_passed':all(checks.values()),
    'stress_covered':len(stress_results)==2,'stress_window_p95_requires_review':True,
    'note':'This fixture does not certify production migration, recovery, history, or the separate 35ms comparison.'}, indent=2))

lines = ['# Local gateway benchmark', '', 'Checkout: '+(out/'checkout-sha.txt').read_text().strip()+' (working changes recorded separately).', '', f'Fixture: PostgreSQL added RTT {rtt_ms} ms; host overload monitor disabled; financial, rate and permission admission enabled.', '', '| API / body / workers | TTFT extra P50/P95 ms | Tail P50/P95 ms | SQL API / worker | TX / req | Redis required / API all / server commands | Fresh conns |', '|---|---:|---:|---:|---:|---:|---:|']
if globals().get('decision_mode', False):
    lines[0] = '# Local Decisions request-path benchmark'
    lines.insert(6, 'Both providers are synthetic loopback mocks. Legacy Jev and newly added official OpenAI dialect are compared on this working tree; this is not an old-version official support or real-provider capacity comparison. Decisions is non-streaming: TTFT-labelled numbers below are complete-response latency, and overhead is a difference of independent quantiles, not paired overhead. Channel admission uses 10000 RPS / 1000 in-flight fixture limits. Streaming gates are not applied to this opt-in mode.')
for row in results:
    g=row['gateway']
    lines.append(f"| {row['api']} / {row['kilobytes']}KB / {row['concurrency']} | {row['ttft_overhead_p50_ms']:.1f} / {row['ttft_overhead_p95_ms']:.1f} | {g['tail_p50_ms']:.1f} / {g['tail_p95_ms']:.1f} | {row['api_sql_per_request']:.2f} / {row['worker_sql_per_request']:.2f} | {row['transactions_per_request']:.2f} | {row['redis_request_dispatches_per_request']:.2f} / {row['redis_api_dispatches_per_request']:.2f} / {row['redis_per_request']:.1f} | {row['new_connection_ratio']:.1%} |")
if stress_results:
    lines += ['', '| 500 RPS / 60 seconds | Achieved RPS | TTFT P95 ms | 15-second window P95 ms | Exact API SQL / per request |', '|---|---:|---:|---|---:|']
    for row in stress_results:
        g = row['gateway']
        windows = ' / '.join(f"{w['ttft_p95_ms']:.1f}" if w['ttft_p95_ms'] is not None else 'unknown' for w in g['window_p95'])
        lines.append(f"| {row['scenario']} | {g['requests_per_second']:.1f} | {g['ttft_p95_ms']:.1f} | {windows} | {row['api_sql_statement_count']} / {row['api_sql_per_request']:.6f} |")
lines += ['', f"Writers paused: {'PASS' if paused['passed'] else 'FAIL'}; {successes} successful requests, API SQL {paused['api_sql_statements']}, jobs SQL {paused['worker_sql_statements']}.", '', f"Settlement visibility: {'PASS' if visibility['passed'] else 'FAIL'}; P99 after client EOF {visibility['p99_commit_after_eof_ms']} ms (limit {visibility['limit_ms']} ms), {len(lags)} exact PostgreSQL commit-timestamp samples.", '', f"Accounting: {'PASS' if accounting['passed'] else 'FAIL'}; {operations} financial operations, {ledger_entries} ledger entries; wallet equality {'PASS' if accounting['financial_balances_passed'] else 'FAIL'}. Drain snapshot: {json.dumps(drained,sort_keys=True)}.", '', 'Reference: [historical baseline](../../../ops/bench/gateway/baseline.md).', '', 'SQL excludes instrumentation and setup roles. Redis counts all fixture commands, including script internals and consumer polling. Failure recovery and replay require their separate tests.']
(out/'results.md').write_text('\n'.join(lines)+'\n')

if not all(checks.values()):
    raise SystemExit('Benchmark acceptance failed: '+', '.join(name for name,passed in checks.items() if not passed))
