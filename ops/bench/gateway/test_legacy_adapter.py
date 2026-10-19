"""Offline contracts for the schema-21 adapter; never start Docker or a gateway."""
import json
import contextlib
import io
import os
import pathlib
import tempfile
import unittest
from unittest.mock import patch

import legacy_setup
import legacy_summary


def result(requests=2, successful=2, ttft=303):
    return {'requests': requests, 'status_codes': {'200': successful, '0': requests - successful},
            'ttft_p50_ms': ttft, 'ttft_p95_ms': ttft + 1, 'tail_max_ms': 3}


def runner(out):
    value = legacy_summary.Runner.__new__(legacy_summary.Runner)
    value.out = pathlib.Path(out)
    value.gateway, value.upstream = 'http://127.0.0.1:10110', 'http://127.0.0.1:10082'
    value.duration, value.keys = '8s', 'example-synthetic-key'
    value.pg, value.redis = 'lazu-gateway-legacy21-test-pg', 'lazu-gateway-legacy21-test-redis'
    value.samples, value.results, value.stress, value.warming = [], [], [], []
    return value


class GuardTests(unittest.TestCase):
    def test_schema21_scopes_ignore_cleanup_and_pending_task_versions(self):
        rows = ['primary:21', 'legacy_seed_cleanup:1',
                'retired_referral_cache:1', 'retired_model_ranks:1']
        self.assertEqual(legacy_setup.validate_schema21(rows), {'primary': 21})
        self.assertEqual(legacy_setup.validate_schema21(rows + ['log:21'], separate_log=True),
                         {'primary': 21, 'log': 21})
        for invalid in ([], ['primary_db:21'], ['primary:20'], ['primary:25'],
                        ['primary:21', 'primary:21'], ['primary:21', 'log:25']):
            with self.assertRaises(ValueError):
                legacy_setup.validate_schema21(invalid)
        with self.assertRaises(ValueError):
            legacy_setup.validate_schema21(rows, separate_log=True)

    def test_fixture_only_accepts_local_base_urls_and_owned_containers(self):
        for value in ('http://127.0.0.1:10110/', 'http://localhost:10110', 'http://[::1]:10110'):
            self.assertEqual(legacy_setup.loopback_url(value), value.rstrip('/'))
        for value in ('https://127.0.0.1:10110', 'http://example.invalid', 'http://user:pass@127.0.0.1',
                      'http://127.0.0.1/api', 'http://127.0.0.1?external=1', 'http://127.0.0.1#endpoint'):
            with self.assertRaises(ValueError):
                legacy_setup.loopback_url(value)
        self.assertEqual(legacy_setup.fixture_container('lazu-gateway-legacy21-test-pg'),
                         'lazu-gateway-legacy21-test-pg')
        for value in ('production-pg', 'lazu-gateway-bench-test-pg', 'lazu-gateway-legacy21-test;sql'):
            with self.assertRaises(ValueError):
                legacy_setup.fixture_container(value)

    def test_private_key_file_restricts_existing_permissions(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / 'keys.txt'
            path.write_text('old')
            path.chmod(0o644)
            legacy_setup.private_write(path, 'example-synthetic-key')
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(path.read_text(), 'example-synthetic-key')

    def test_case_selection_retains_shared_matrix_and_rejects_unknown_duplicates(self):
        self.assertEqual(len(legacy_summary.selected_cases('')), 12)
        selected = legacy_summary.selected_cases('responses:1:1,chat:600:8')
        self.assertEqual(selected, [('responses', 1, 1), ('chat', 600, 8)])
        self.assertEqual([legacy_summary.DEFAULT_CASES.index(row) for row in selected], [0, 9])
        for selection in ('responses:1:1,responses:1:1', 'responses:2:1', 'responses:1', 'other:1:1'):
            with self.assertRaises(ValueError):
                legacy_summary.selected_cases(selection)


class ObservationTests(unittest.TestCase):
    def test_partial_or_failed_http_does_not_claim_acceptance(self):
        partial = legacy_summary.comparison(result(), result(3, 2, 330))
        self.assertFalse(partial['comparison_complete'])
        self.assertFalse(partial['gateway_complete_success'])
        self.assertEqual(partial['gateway_success_rate'], 2 / 3)
        self.assertEqual(partial['ttft_overhead_p50_ms'], 27)
        none = legacy_summary.comparison(result(), result(3, 0, 0))
        self.assertIsNone(none['ttft_overhead_p50_ms'])
        self.assertFalse(legacy_summary.complete_success(result(0, 0)))
        invalid_direct = legacy_summary.comparison(result(2, 1), result())
        self.assertIsNone(invalid_direct['ttft_overhead_p50_ms'])
        for unavailable in legacy_summary.unsupported_metrics().values():
            self.assertFalse(unavailable['supported'])
            self.assertIsNone(unavailable['value'])

    def test_same_shared_tool_and_load_definition(self):
        value = runner('/unused/local-output')
        command = value.command('responses', 1, 750, 'gateway', duration='60s', rps=500,
                                samples='/unused/local-output/samples-00.jsonl')
        self.assertEqual(command[0], '/unused/local-output/bench')
        for flag, expected in (('-api', 'responses'), ('-kb', '1'), ('-c', '750'), ('-d', '60s'),
                               ('-rps', '500'), ('-url', value.gateway + '/v1/responses'),
                               ('-samples', '/unused/local-output/samples-00.jsonl')):
            self.assertEqual(command[command.index(flag) + 1], expected)
        self.assertNotIn('-expected-content-events', command)  # shared tool default: complete 100 events
        direct = value.command('chat', 600, 8, 'direct')
        self.assertEqual(direct[direct.index('-url') + 1], value.upstream + '/v1/chat/completions')
        self.assertEqual(direct[direct.index('-keys') + 1], 'mock')

    def test_matrix_default_never_implicitly_runs_500_rps(self):
        with tempfile.TemporaryDirectory() as directory:
            value = runner(directory)
            value.case_keys = [['example-synthetic-case-key'] for _ in range(12)]
            calls = []
            def bench(*args, **kwargs):
                calls.append((args, kwargs))
                return result()
            value.bench = bench
            value.counters = lambda: {'api_process_sql': 0, 'api_process_transactions': 0, 'redis_server_commands': 0}
            value.upstream_stats = lambda: {'new_conns': 0, 'requests': 2}
            value.finish = lambda: None
            with patch.dict(os.environ, {}, clear=True), contextlib.redirect_stdout(io.StringIO()):
                value.run()
            self.assertEqual(len(value.results), 12)
            self.assertEqual(value.stress, [])
            self.assertFalse(any(kwargs.get('rps', 0) > 0 for _args, kwargs in calls))

    def test_missing_or_duplicate_success_ids_reject_the_measurement(self):
        with tempfile.TemporaryDirectory() as directory:
            value = runner(directory)
            response = type('Process', (), {'returncode': 0, 'stdout': json.dumps(result())})()
            def execute(command, **_kwargs):
                path = pathlib.Path(command[command.index('-samples') + 1])
                path.write_text(json.dumps({'request_id': 'same-id'}) + '\n'
                                + json.dumps({'request_id': 'same-id'}) + '\n')
                return response
            with patch('legacy_summary.subprocess.run', side_effect=execute):
                with self.assertRaisesRegex(ValueError, 'missing or duplicated'):
                    value.bench('responses', 1, 1, 'gateway', measured=True)
            def truncated(command, **_kwargs):
                pathlib.Path(command[command.index('-samples') + 1]).write_text('')
                return response
            with patch('legacy_summary.subprocess.run', side_effect=truncated):
                with self.assertRaisesRegex(ValueError, 'coverage mismatch'):
                    value.bench('responses', 1, 1, 'gateway', measured=True)

    def test_report_preserves_failed_case_and_marks_modern_gates_unavailable(self):
        with tempfile.TemporaryDirectory() as directory:
            value = runner(directory)
            value.results = [{'api': 'responses', 'kilobytes': 1, 'concurrency': 1,
                              'gateway': result(3, 0, 0), **legacy_summary.comparison(result(), result(3, 0, 0)),
                              'api_process_sql_per_request': 15}]
            value.drain_status = lambda: {'relay_event_pending': 0, 'relay_event_lag': 0,
                                          'budget_reservations': 0, 'unresolved_settlement_failures': 0,
                                          'unresolved_settlement_outbox': 0}
            def sql(query):
                if 'wallet_ledgers' in query:
                    return '\n'.join(f'{index}|100|100' for index in range(10))
                return '[]'
            value.sql = sql
            value.finish()
            observed = json.loads((value.out / 'legacy-observation.json').read_text())
            self.assertFalse(observed['observed_http_success'])
            self.assertIsNone(observed['release_acceptance'])
            self.assertIn('| 3 / 0 | N/A |', (value.out / 'results.md').read_text())
            self.assertTrue(json.loads((value.out / 'accounting.json').read_text())['core_wallets_equal'])
            self.assertFalse(observed['unsupported']['redis_permanent_ledger_accounting']['supported'])

    def test_legacy_queue_formats_and_missing_consumer(self):
        value = runner('/unused/local-output')
        value.sql = lambda _query: '0'
        shapes = ([{'name': 'lazu-relay-event-workers', 'pending': 3, 'lag': 4}],
                  [['name', 'lazu-relay-event-workers', 'pending', 3, 'lag', 4]])
        for shape in shapes:
            value.redis_json = lambda command, *_args: 1 if command == 'EXISTS' else shape
            self.assertEqual(value.drain_status()['relay_event_pending'], 3)
            self.assertEqual(value.drain_status()['relay_event_lag'], 4)
        value.redis_json = lambda command, *_args: 1 if command == 'EXISTS' else []
        with self.assertRaisesRegex(RuntimeError, 'no measurable consumer'):
            value.drain_status()
        value.redis_json = lambda command, *_args: 1 if command == 'EXISTS' else [{'name': 'lazu-relay-event-workers', 'pending': 0, 'lag': None}]
        with self.assertRaisesRegex(RuntimeError, 'no measurable consumer'):
            value.drain_status()


class FixtureTests(unittest.TestCase):
    def test_old_console_contract_synthetic_opening_and_distinct_cohorts(self):
        class FakeFixture(legacy_setup.Fixture):
            queries, created_tokens, api_requests = [], [], []
            def session(self, username, _password):
                return {'user': username}
            def api(self, headers, path, body=None, method=None):
                self.api_requests.append((path, body, method))
                if path == '/api/vendors/':
                    return {'internal_id': 7}
                if path == '/api/token/':
                    key = f"example-{headers['user']}-{body['name']}"
                    self.created_tokens.append(key)
                    return {'key': key}
                return {}
            def sql(self, query):
                self.queries.append(query)
                if query.startswith('SELECT id'):
                    return str(int(query.split("username='perf")[1].split("'")[0]) + 1)
                return ''
        with tempfile.TemporaryDirectory() as directory:
            fixture = FakeFixture('http://127.0.0.1:10110', 'http://127.0.0.1:10082',
                                  'lazu-gateway-legacy21-test-pg', 'lazu-gateway-legacy21-test-redis')
            with patch.dict(os.environ, {'ROOT_USERNAME': 'example-root', 'ROOT_PASSWORD': 'example-root-password'}), \
                    patch('legacy_setup.subprocess.check_output', return_value=b'1') as redis:
                fixture.create(pathlib.Path(directory) / 'keys.txt')
            self.assertEqual(redis.call_count, 10)
            self.assertTrue(all(call.args[0][-2] == 'DEL' for call in redis.call_args_list))
            cases = json.loads((pathlib.Path(directory) / 'case-keys.json').read_text())
            self.assertEqual(len(cases), 12)
            self.assertTrue(all(len(case) == 10 for case in cases))
            self.assertEqual(len({key for case in cases for key in case}), 120)
            self.assertEqual(len(set(fixture.created_tokens)), 139)
            self.assertEqual(len((pathlib.Path(directory) / 'same-project-keys.txt').read_text().split(',')), 10)
            joined = '\n'.join(fixture.queries)
            self.assertNotIn('credit_seq', joined)
            self.assertNotIn('redis_ledger', joined)
            self.assertEqual(joined.count("'synthetic_initial_credit'"), 10)
            self.assertEqual(joined.count("'topup'"), 10)
            self.assertNotIn("'top_up'", joined)
            self.assertTrue(all(path.stat().st_mode & 0o777 == 0o600 for path in pathlib.Path(directory).iterdir()))
            sell_price = next(body for path, body, _method in fixture.api_requests if path == '/api/pricing/sell_prices')
            self.assertEqual((sell_price['input_per_mtok'], sell_price['output_per_mtok']), (1, 2))
            monitor = next(body for path, body, _method in fixture.api_requests
                           if path == '/api/option/' and body['key'] == 'performance_setting.monitor_enabled')
            self.assertEqual(monitor['value'], 'false')


if __name__ == '__main__':
    unittest.main()
