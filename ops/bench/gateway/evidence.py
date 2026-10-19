"""Fail-closed validation for synthetic benchmark evidence."""

def dispatch_counters(lines, allow_unseen_required=False):
    enabled = next((float(line.rsplit(' ', 1)[1]) for line in lines
                    if line.startswith('lazu_hot_path_metrics_enabled ')), None)
    if enabled != 1:
        raise ValueError('Hot-path instrumentation is absent or disabled')
    values = {}
    for metric, name in (('redis.client_dispatches', 'redis_api_dispatches'),
                         ('redis.request_dispatches', 'redis_request_dispatches')):
        prefix = 'lazu_hot_path_counter_total{name="' + metric + '"}'
        value = next((int(float(line.rsplit(' ', 1)[1])) for line in lines if line.startswith(prefix)), None)
        if value is None and not (allow_unseen_required and metric == 'redis.request_dispatches'):
            raise ValueError('Required Redis instrumentation is absent: ' + metric)
        values[name] = value
    return values


def require_sample_coverage(samples, successful):
    if len(samples) != successful:
        raise ValueError(f'Request-ID sample coverage mismatch: {len(samples)} / {successful}')
    ids = [sample.get('request_id') for sample in samples]
    if any(not isinstance(value, str) or not value for value in ids) or len(set(ids)) != len(ids):
        raise ValueError('Request-ID samples are missing or duplicated')


def clock_offset_bounds(samples):
    if not samples or any(row['host_after_ms'] < row['host_before_ms'] for row in samples):
        raise ValueError('Invalid host/PostgreSQL clock calibration')
    # SQL integer rounding and host sample granularity each need a 1 ms margin.
    lower = min(row['postgres_ms'] - row['host_after_ms'] - 2 for row in samples)
    upper = max(row['postgres_ms'] - row['host_before_ms'] + 2 for row in samples)
    if lower < -100 or upper > 100:
        raise ValueError(f'Host/PostgreSQL clock alignment is not bounded within 100 ms: {lower}..{upper}')
    return {'postgres_minus_host_lower_ms': lower, 'postgres_minus_host_upper_ms': upper,
            'samples': samples}


def commit_latency_upper_ms(postgres_commit_ms, host_eof_ms, bounds):
    return max(0, postgres_commit_ms - host_eof_ms - bounds['postgres_minus_host_lower_ms'])


def accounting_checks(drained, wallets, operations, ledger_entries):
    drain_passed = all(value == 0 for value in drained.values())
    financial_passed = (len(wallets) == 10 and all(row['equal'] for row in wallets)
                        and operations == ledger_entries and operations > 0)
    return {'drain_passed': drain_passed, 'financial_balances_passed': financial_passed,
            'passed': drain_passed and financial_passed}
