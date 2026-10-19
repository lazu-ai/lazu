"""Exercise the real finalization block after a controlled drain failure."""
import ast
import contextlib
import io
import json
import os
import pathlib
import re
import tempfile
import types
import unittest

from evidence import accounting_checks, clock_offset_bounds, commit_latency_upper_ms, require_sample_coverage


class FailureExtractionTests(unittest.TestCase):
    def test_telemetry_backlog_keeps_all_evidence_and_fails_acceptance(self):
        source = pathlib.Path(__file__).with_name('summary.py').read_text()
        tree = ast.parse(source)
        start = next(i for i, node in enumerate(tree.body)
                     if isinstance(node, ast.For) and isinstance(node.iter, ast.Call)
                     and isinstance(node.iter.func, ast.Name) and node.iter.func.id == 'range'
                     and len(node.iter.args) == 1 and isinstance(node.iter.args[0], ast.Constant)
                     and node.iter.args[0].value == 60)
        finalizer = compile(ast.Module(body=tree.body[start:], type_ignores=[]), '<real-summary-finalization>', 'exec')
        with tempfile.TemporaryDirectory() as folder:
            out = pathlib.Path(folder)
            (out/'checkout-sha.txt').write_text('synthetic-test\n')
            samples = []
            for number in range(4):
                path = out/f'samples-{number:02d}.jsonl'
                path.write_text(json.dumps({'request_id': f'sample-{number}', 'eof_ms': 1000})+'\n')
                samples.append(path)
            gateway = {'status_codes': {'200': 1}, 'requests': 1, 'tail_max_ms': 1,
                       'tail_p50_ms': 1, 'tail_p95_ms': 1, 'offered_rps': 500,
                       'duration_seconds': 60, 'requests_per_second': 500,
                       'ttft_p95_ms': 303, 'window_p95': [{'ttft_p95_ms': 303}]}
            row = {'api': 'responses', 'kilobytes': 1, 'concurrency': 1, 'gateway': gateway,
                   'api_sql_statement_count': 0, 'api_sql_per_request': 0,
                   'worker_sql_per_request': 30, 'transactions_per_request': 2,
                   'redis_request_dispatches_per_request': 3, 'redis_api_dispatches_per_request': 4,
                   'redis_per_request': 12, 'ttft_overhead_p50_ms': 3,
                   'ttft_overhead_p95_ms': 3, 'new_connection_ratio': 0}
            def sql(query, database='lazu'):
                if 'GROUP BY u.id' in query:
                    return '\n'.join(f'{number}|999|999' for number in range(1, 11))
                if 'count(*)' in query: return '4'
                if 'pg_xact_commit_timestamp' in query:
                    return '\n'.join(f'sample-{number}|1050' for number in range(3))
                if 'FROM logs WHERE' in query:
                    return json.dumps([{'request_id': 'sample-0', 'gateway_pre_ms': 3,
                                        'upstream_ttfb_ms': 300, 'gateway_post_ms': 1}])
                return '[]'
            namespace = {'out': out, 'sql': sql, 'redis_value': lambda *args: 999,
                         'drain_status': lambda: {'financial_pending': 0, 'telemetry_lag': 51252},
                         'time': types.SimpleNamespace(sleep=lambda _: None),
                         'calibrate_clock': lambda _: None,
                         'clock_samples': [{'host_before_ms': 1000, 'host_after_ms': 1000, 'postgres_ms': 1000}],
                         'sample_files': samples, 'results': [row],
                         'stress_results': [{'scenario': str(i), 'gateway': gateway,
                                             'api_sql_statement_count': 0, 'api_sql_per_request': 0} for i in range(2)],
                         'paused': {'passed': True, 'gateway': gateway, 'api_sql_statements': 0, 'worker_sql_statements': 0},
                         'successes': 1, 'json': json, 'os': os, 're': re,
                         'clock_offset_bounds': clock_offset_bounds,
                         'commit_latency_upper_ms': commit_latency_upper_ms,
                         'require_sample_coverage': require_sample_coverage,
                         'accounting_checks': accounting_checks}
            with contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(SystemExit, 'accounting'):
                exec(finalizer, namespace)
            accounting = json.loads((out/'accounting.json').read_text())
            self.assertTrue(accounting['financial_balances_passed'])
            self.assertFalse(accounting['passed'])
            acceptance = json.loads((out/'acceptance.json').read_text())
            self.assertFalse(acceptance['all_numeric_checks_passed'])
            self.assertFalse(acceptance['checks']['accounting'])
            self.assertEqual(json.loads((out/'settlement-visibility.json').read_text())['samples'], 3)
            self.assertTrue((out/'sql-statements.json').exists())
            self.assertTrue((out/'timing-profiles.json').exists())
            facts = json.loads((out/'timing-facts.json').read_text())
            self.assertEqual(facts[0]['facts'][0]['request_id'], 'sample-0')
            self.assertEqual(facts[0]['facts'][0]['gateway_post_ms'], 1)
            profile = json.loads((out/'timing-profiles.json').read_text())[0]
            self.assertEqual(profile['gateway_post_ms']['max'], 1)
            self.assertIn('Accounting: FAIL', (out/'results.md').read_text())


if __name__ == '__main__':
    unittest.main()
