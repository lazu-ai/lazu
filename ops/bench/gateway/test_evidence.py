import unittest
from evidence import dispatch_counters, require_sample_coverage, clock_offset_bounds, commit_latency_upper_ms, accounting_checks


class EvidenceTests(unittest.TestCase):
    def test_balanced_finances_cannot_hide_telemetry_backlog(self):
        wallets = [{'equal': True} for _ in range(10)]
        failed = accounting_checks({'financial_pending': 0, 'telemetry_lag': 51252}, wallets, 500, 500)
        self.assertTrue(failed['financial_balances_passed'])
        self.assertFalse(failed['passed'])
        self.assertTrue(accounting_checks({'financial_pending': 0, 'telemetry_lag': 0}, wallets, 500, 500)['passed'])
        self.assertFalse(accounting_checks({'financial_pending': 0, 'telemetry_lag': 0}, wallets, 499, 500)['passed'])

    def test_metrics_cannot_silently_become_zero(self):
        enabled = ['lazu_hot_path_metrics_enabled 1',
                   'lazu_hot_path_counter_total{name="redis.client_dispatches"} 17']
        with self.assertRaises(ValueError):
            dispatch_counters(enabled)
        self.assertIsNone(dispatch_counters(enabled, True)['redis_request_dispatches'])
        with self.assertRaises(ValueError):
            dispatch_counters(['lazu_hot_path_metrics_enabled 0'])
        self.assertEqual(dispatch_counters(enabled + ['lazu_hot_path_counter_total{name="redis.request_dispatches"} 9'])['redis_request_dispatches'], 9)

    def test_every_success_requires_its_unique_sample(self):
        with self.assertRaises(ValueError):
            require_sample_coverage([{'request_id': 'one'}], 2)
        with self.assertRaises(ValueError):
            require_sample_coverage([{'request_id': ''}], 1)
        with self.assertRaises(ValueError):
            require_sample_coverage([{'request_id': 'same'}, {'request_id': 'same'}], 2)
        require_sample_coverage([{'request_id': 'one'}, {'request_id': 'two'}], 2)

    def test_clock_skew_is_bounded_and_latency_is_conservative(self):
        bounds = clock_offset_bounds([{'host_before_ms': 1000, 'host_after_ms': 1010, 'postgres_ms': 1020}])
        # True offset may be anywhere in 8..22 ms. Never claim the midpoint.
        self.assertEqual(commit_latency_upper_ms(1150, 1100, bounds), 42)
        self.assertEqual(commit_latency_upper_ms(1000, 1100, bounds), 0)
        with self.assertRaises(ValueError):
            clock_offset_bounds([{'host_before_ms': 1000, 'host_after_ms': 1010, 'postgres_ms': 1300}])


if __name__ == '__main__':
    unittest.main()
