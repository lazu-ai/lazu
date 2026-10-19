"""Measure schema 21 with the unchanged shared mock/bench/pgdelay binaries."""
import json
import os
import pathlib
import subprocess
import sys
import time
import urllib.request

from evidence import require_sample_coverage
from legacy_setup import fixture_container, loopback_url

DEFAULT_CASES = [(api, kb, workers) for api in ('responses', 'chat')
                 for kb, workers in ((1, 1), (100, 1), (600, 1), (600, 8), (1, 32), (1, 50))]


def selected_cases(raw):
    if not raw:
        return DEFAULT_CASES[:]
    cases = []
    for entry in raw.split(','):
        api, kb, workers = entry.split(':')
        row = api, int(kb), int(workers)
        if row not in DEFAULT_CASES or row in cases:
            raise ValueError('Legacy cases must select distinct entries from the shared 12-case matrix')
        cases.append(row)
    return cases


def unsupported_metrics():
    return {name: {'supported': False, 'value': None, 'reason': 'Absent from schema-21 runtime; not measured'}
            for name in ('required_redis_dispatches', 'api_only_sql_excluding_consumer',
                         'separate_worker_sql', 'redis_permanent_ledger_accounting',
                         'core_settlement_commit_visibility', 'gateway_stage_timing', 'writers_paused_acceptance')}


def complete_success(result):
    return result['requests'] > 0 and result['status_codes'].get('200', 0) == result['requests']


def comparison(direct, gateway):
    direct_ok = complete_success(direct)
    successful = gateway['status_codes'].get('200', 0)
    return {
        'direct_complete_success': direct_ok, 'gateway_complete_success': complete_success(gateway),
        'gateway_success_rate': successful / max(1, gateway['requests']),
        'comparison_complete': direct_ok and complete_success(gateway),
        'percentiles_use_only_successful_requests': True,
        'ttft_overhead_p50_ms': gateway['ttft_p50_ms'] - direct['ttft_p50_ms'] if direct_ok and successful else None,
        'ttft_overhead_p95_ms': gateway['ttft_p95_ms'] - direct['ttft_p95_ms'] if direct_ok and successful else None,
    }


class Runner:
    def __init__(self, out):
        self.out = pathlib.Path(out)
        self.pg = fixture_container(os.environ['LAZU_BENCH_PG_CONTAINER'])
        self.redis = fixture_container(os.environ['LAZU_BENCH_REDIS_CONTAINER'])
        self.gateway = loopback_url(os.environ['LAZU_BENCH_GATEWAY_URL'])
        self.upstream = loopback_url(os.environ['LAZU_BENCH_UPSTREAM_URL'])
        self.duration = os.environ.get('LAZU_BENCH_DURATION', '8s')
        self.keys = (self.out / 'keys.txt').read_text().strip()
        self.case_keys = json.loads((self.out / 'case-keys.json').read_text())
        self.samples, self.results, self.stress, self.warming = [], [], [], []

    def save(self, name, payload):
        (self.out / name).write_text(json.dumps(payload, indent=2) + '\n')

    def sql(self, query):
        return subprocess.check_output(['docker', 'exec', self.pg, 'psql', '-U', 'lazu', '-d', 'lazu',
                                        '-v', 'ON_ERROR_STOP=1', '-Atc', query], text=True).strip()

    def redis_json(self, *args):
        return json.loads(subprocess.check_output(['docker', 'exec', self.redis, 'redis-cli', '--json', *args], text=True))

    def counters(self):
        values = self.sql("SELECT COALESCE(sum(calls),0)::bigint,COALESCE(sum(CASE WHEN query ~* '^(BEGIN|START TRANSACTION)' THEN calls ELSE 0 END),0)::bigint FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname='lazu_bench_legacy'")
        calls, transactions = map(int, values.split('|'))
        raw = subprocess.check_output(['docker', 'exec', self.redis, 'redis-cli', 'INFO', 'commandstats'], text=True)
        commands = sum(int(line.split('calls=')[1].split(',')[0]) for line in raw.splitlines() if line.startswith('cmdstat_'))
        return {'api_process_sql': calls, 'api_process_transactions': transactions, 'redis_server_commands': commands}

    def upstream_stats(self):
        with urllib.request.urlopen(self.upstream + '/stats', timeout=10) as response:
            return {key: int(value) for key, value in (part.split('=') for part in response.read().decode().split())}

    def command(self, api, kb, workers, target, keys=None, duration=None, rps=0, samples=None):
        endpoint = '/v1/responses' if api == 'responses' else '/v1/chat/completions'
        command = [str(self.out / 'bench'), '-api', api, '-kb', str(kb), '-c', str(workers),
                   '-d', duration or self.duration, '-rps', str(rps),
                   '-url', (self.gateway if target == 'gateway' else self.upstream) + endpoint,
                   '-keys', (keys or self.keys) if target == 'gateway' else 'mock', '-json',
                   '-label', f'legacy21-{api}-{kb}KB-c{workers}-{target}']
        if samples is not None:
            command += ['-samples', str(samples)]
        return command

    def bench(self, api, kb, workers, target, keys=None, duration=None, rps=0, measured=False):
        sample = None
        if measured and target == 'gateway':
            sample = self.out / f'samples-{len(self.samples):02d}.jsonl'
            self.samples.append(sample)
        run = subprocess.run(self.command(api, kb, workers, target, keys, duration, rps, sample),
                             capture_output=True, text=True, check=False)
        if run.returncode:
            raise RuntimeError(f'Shared benchmark executable exited {run.returncode}')
        result = json.loads(run.stdout)
        if sample is not None:
            rows = [json.loads(line) for line in sample.read_text().splitlines()]
            require_sample_coverage(rows, result['status_codes'].get('200', 0))
        return result

    def warm(self, api, kb, workers, keys, phase, duration='2s'):
        result = self.bench(api, kb, workers, 'gateway', keys, duration)
        self.warming.append({'phase': phase, 'gateway': result, 'complete_success': complete_success(result)})
        self.save('cold-warm.json', self.warming)
        if not complete_success(result):
            raise RuntimeError(f'Legacy warm fixture failed during {phase}; preserved in cold-warm.json')

    def deltas(self, before, after, requests):
        row = {}
        for name in before:
            row[name + '_statement_count' if name == 'api_process_sql' else name + '_count'] = after[name] - before[name]
            row[name + '_per_request'] = (after[name] - before[name]) / max(1, requests)
        return row

    def drain_status(self):
        exists = self.redis_json('EXISTS', 'lazu:relay_events')
        if exists:
            groups = self.redis_json('XINFO', 'GROUPS', 'lazu:relay_events')
            decoded = [row if isinstance(row, dict) else dict(zip(row[::2], row[1::2])) for row in groups]
            group = next((row for row in decoded if row.get('name') == 'lazu-relay-event-workers'), None)
            if group is None or group.get('lag') is None:
                raise RuntimeError('Legacy relay stream has no measurable consumer group')
            pending, lag = int(group['pending']), int(group['lag'])
        else:
            pending, lag = 0, 0
        return {'relay_event_pending': pending, 'relay_event_lag': lag,
                'budget_reservations': int(self.sql('SELECT count(*) FROM spend_budget_reservations')),
                'unresolved_settlement_failures': int(self.sql("SELECT count(*) FROM settlement_failures WHERE status<>'resolved'")),
                'unresolved_settlement_outbox': int(self.sql("SELECT count(*) FROM settlement_outboxes WHERE status<>'resolved'"))}

    def finish(self):
        drain = None
        for _ in range(60):
            drain = self.drain_status()
            if all(value == 0 for value in drain.values()):
                break
            time.sleep(1)
        wallets = []
        for line in self.sql("SELECT u.id,u.quota,COALESCE(sum(w.delta_microusd),0)::bigint FROM users u LEFT JOIN wallet_ledgers w ON w.user_id=u.id WHERE u.username ~ '^perf[0-9]+$' GROUP BY u.id,u.quota ORDER BY u.id").splitlines():
            identity, balance, movement = map(int, line.split('|'))
            wallets.append({'payer_id': identity, 'core_quota': balance, 'wallet_ledger_sum': movement, 'equal': balance == movement})
        accounting = {'scope': 'schema21 Core wallet projection and old queues only; no modern Redis financial ledger',
                      'drain': drain, 'wallets': wallets,
                      'core_wallets_equal': len(wallets) == 10 and all(row['equal'] for row in wallets),
                      'legacy_queues_drained': all(value == 0 for value in drain.values())}
        self.save('accounting.json', accounting)
        self.save('unsupported-metrics.json', unsupported_metrics())
        self.save('sql-statements.json', json.loads(self.sql("SELECT COALESCE(json_agg(rows)::text,'[]') FROM (SELECT s.calls,s.query FROM pg_stat_statements s JOIN pg_roles r ON r.oid=s.userid WHERE r.rolname='lazu_bench_legacy' ORDER BY s.calls DESC) rows")))
        observed = all(row['gateway_complete_success'] for row in self.results) and all(complete_success(row['gateway']) for row in self.stress)
        self.save('legacy-observation.json', {'run_kind': 'historical-schema21-comparison', 'schema_version': 21,
                  'source': '6a29da9e7', 'matrix_cases': len(self.results), 'stress_cases': len(self.stress),
                  'observed_http_success': observed, 'release_acceptance': None,
                  'release_acceptance_reason': 'Historical comparison; modern 11-gate acceptance is not applicable',
                  'sql_scope': 'API process including its in-process consumer and periodic work',
                  'redis_scope': 'Server commands including Lua-internal and collector commands; not network dispatches',
                  'fixture_capacity_configuration': 'CPU admission monitor disabled to match modern synthetic fixture; not production capacity configuration',
                  'unsupported': unsupported_metrics()})
        lines = ['# Legacy schema-21 synthetic comparison', '',
                 'Shared mock, benchmark and PostgreSQL-delay executables. This is a historical comparison, not modern release acceptance.', '',
                 'SQL counts cover the API process, its original in-process consumer and periodic work. Redis counts are server commands, not dispatches.', '',
                 'The CPU admission monitor is disabled in both synthetic fixtures. This is not production capacity configuration.', '',
                 '| API | KB | Workers | Requests / 200 | TTFT delta P50 ms | Tail MAX ms | API-process SQL/request |',
                 '| --- | ---: | ---: | ---: | ---: | ---: | ---: |']
        for row in self.results:
            value = row['ttft_overhead_p50_ms']
            shown = f'{value:.3f}' if value is not None else 'N/A'
            measured = row['gateway']
            lines.append(f"| {row['api']} | {row['kilobytes']} | {row['concurrency']} | {measured['requests']} / {measured['status_codes'].get('200',0)} | {shown} | {measured['tail_max_ms']:.3f} | {row['api_process_sql_per_request']:.3f} |")
        lines += ['', 'Percentiles include successful complete responses only; see status codes and comparison_complete before interpreting differences.', '',
                  f"Stress: {json.dumps(self.stress)}", '',
                  f"Core wallet equality: {accounting['core_wallets_equal']}; old queues drained: {accounting['legacy_queues_drained']}.", '',
                  'Modern required Redis dispatches, permanent Redis ledger, Core settlement commit visibility, split worker SQL, writer-pause acceptance and request timing stages: N/A.']
        (self.out / 'results.md').write_text('\n'.join(lines) + '\n')

    def run(self):
        for index, key in enumerate(self.keys.split(',')):
            self.warm('responses', 1, 1, key, f'cold-payer-{index}', '200ms')
        for api, kb, workers in selected_cases(os.environ.get('LAZU_BENCH_LEGACY_CASES', '')):
            direct = self.bench(api, kb, workers, 'direct')
            if not complete_success(direct):
                self.save('failed-direct.json', direct)
                raise RuntimeError('Shared direct fixture produced failed responses')
            cohort = ','.join(self.case_keys[DEFAULT_CASES.index((api, kb, workers))])
            self.warm(api, kb, max(workers, 10), cohort, f'{api}-{kb}KB-c{workers}')
            self.upstream_stats()
            before = self.counters()
            measured = self.bench(api, kb, workers, 'gateway', cohort, measured=True)
            after, connections = self.counters(), self.upstream_stats()
            row = {'api': api, 'kilobytes': kb, 'concurrency': workers, 'direct': direct, 'gateway': measured,
                   **comparison(direct, measured), **self.deltas(before, after, measured['requests']),
                   'new_connection_ratio': connections['new_conns'] / max(1, connections['requests'])}
            self.results.append(row)
            self.save('results.json', self.results)
            print(f"Legacy {api} {kb}KB c{workers}: requests={measured['requests']}; status={measured['status_codes']}; deltaP50={row['ttft_overhead_p50_ms']}", flush=True)
        if os.environ.get('LAZU_BENCH_STRESS_ENABLED', 'false').lower() == 'true':
            for scenario, filename in [('single key', 'single-key.txt'), ('same project / ten keys', 'same-project-keys.txt')]:
                cohort = (self.out / filename).read_text().strip()
                self.warm('responses', 1, 10, cohort, scenario)
                before = self.counters()
                measured = self.bench('responses', 1, 750, 'gateway', cohort, '60s', 500, measured=True)
                row = {'scenario': scenario, 'gateway': measured, 'gateway_complete_success': complete_success(measured),
                       **self.deltas(before, self.counters(), measured['requests'])}
                self.stress.append(row)
                self.save('stress.json', self.stress)
                print(f"Legacy stress {scenario}: requests={measured['requests']}; status={measured['status_codes']}", flush=True)
        self.finish()


def main():
    runner = Runner(sys.argv[1])
    try:
        runner.run()
    except Exception as error:
        runner.save('measurement-error.json', {'error': type(error).__name__, 'message': str(error)[:500],
                                             'completed': False, 'release_acceptance': None})
        raise


if __name__ == '__main__':
    main()
