"""Create only synthetic schema-21 fixtures through the old console API."""
import json
import os
import pathlib
import secrets
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


def validate_schema21(rows, separate_log=False):
    """Validate application scopes, not unrelated one-time migration markers.

    Exact 6a29da9e7 uses primary/log. With empty LOG_SQL_DSN, InitLogDB
    shares the primary handle and never creates a separate log marker.
    schema_versions also contains legacy_seed_cleanup and retired_* tasks
    whose version is 1, not the application schema version.
    """
    versions = {}
    for row in rows:
        scope, separator, version = row.strip().partition(':')
        if scope not in ('primary', 'log'):
            continue
        if not separator or scope in versions or version != '21':
            raise ValueError('Legacy fixture application scope is not schema 21: ' + scope)
        versions[scope] = 21
    required = {'primary', 'log'} if separate_log else {'primary'}
    if not required.issubset(versions):
        raise ValueError('Legacy fixture is missing its schema-21 application scope')
    return versions


def loopback_url(value):
    parsed = urllib.parse.urlparse(value)
    if (parsed.scheme != 'http' or parsed.hostname not in ('127.0.0.1', 'localhost', '::1')
            or parsed.username or parsed.password or parsed.path not in ('', '/')
            or parsed.query or parsed.fragment):
        raise ValueError('Legacy fixtures only accept HTTP loopback URLs')
    return value.rstrip('/')


def fixture_container(value):
    if not value.startswith('lazu-gateway-legacy21-') or not all(c.isalnum() or c in '-_' for c in value):
        raise ValueError('Expected an owned lazu-gateway-legacy21 container')
    return value


def private_write(path, value):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, 'w') as handle:
        handle.write(value)
    os.chmod(path, 0o600)


class Fixture:
    def __init__(self, gateway, upstream, pg, redis):
        self.gateway, self.upstream = loopback_url(gateway), loopback_url(upstream)
        self.pg, self.redis = fixture_container(pg), fixture_container(redis)

    def raw(self, path, body=None, method=None, headers=None):
        request = urllib.request.Request(
            self.gateway + path,
            data=json.dumps(body).encode() if body is not None else None,
            headers={'Content-Type': 'application/json', **(headers or {})}, method=method)
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                payload = response.read()
                return response.status, json.loads(payload) if payload else {}, response.headers
        except urllib.error.HTTPError as error:
            # Never echo a login body or credential-bearing response.
            raise RuntimeError(f'Legacy fixture API {path} returned HTTP {error.code}') from None

    def session(self, username, password):
        status, body, headers = self.raw('/api/user/login', {'username': username, 'password': password})
        cookie = headers.get('Set-Cookie')
        if status != 200 or not body.get('success') or not cookie:
            raise RuntimeError('Legacy fixture login failed')
        return {'Cookie': cookie.split(';')[0], 'X-Lazu-User': str(body['data']['id'])}

    def api(self, headers, path, body=None, method=None):
        status, payload, _ = self.raw(path, body, method, headers)
        if status != 200 or payload.get('success') is False:
            raise RuntimeError(f'Legacy fixture API rejected {path}')
        return payload.get('data', payload)

    def sql(self, query):
        return subprocess.check_output(
            ['docker', 'exec', self.pg, 'psql', '-U', 'lazu', '-d', 'lazu', '-v', 'ON_ERROR_STOP=1', '-Atc', query],
            text=True).strip()

    def create(self, destination):
        destination = pathlib.Path(destination)
        destination.parent.mkdir(parents=True, exist_ok=True)
        root = self.session(os.environ['ROOT_USERNAME'], os.environ['ROOT_PASSWORD'])
        port = urllib.parse.urlparse(self.upstream).port or 80
        for key, value in {
            # Match the modern synthetic fixture's CPU admission configuration.
            'performance_setting.monitor_enabled': 'false',
            'fetch_setting.allow_private_ip': 'true',
            'fetch_setting.allowed_ports': json.dumps(['80', '443', str(port)]),
        }.items():
            self.api(root, '/api/option/', {'key': key, 'value': value}, 'PUT')
        vendor = self.api(root, '/api/vendors/', {'name': 'MockAI', 'status': 1, 'description': 'synthetic benchmark'})
        if not isinstance(vendor, dict) or not vendor.get('internal_id'):
            raise RuntimeError('Schema-21 vendor creation did not return an internal identity')
        self.api(root, '/api/channel/', {'mode': 'single', 'channel': {
            'type': 1, 'name': 'Mock OpenAI', 'models': 'gpt-mock', 'group': 'direct', 'source_type': 'official',
            'status': 1, 'priority': 30, 'weight': 10, 'auto_ban': 0, 'base_url': self.upstream,
            'setting': '{}', 'settings': json.dumps({'observe_new_routes': False, 'protocol_dialect': 'openai',
                                                    'capabilities': {'supports_responses': True}}),
            'key': 'example-mock-upstream-key'}})
        self.api(root, '/api/models/', {
            'model_name': 'gpt-mock', 'vendor_id': vendor.get('internal_id'), 'description': 'synthetic benchmark',
            'status': 1, 'lifecycle_status': 'active', 'name_rule': 0, 'source': 'manual', 'sync_official': 0,
            'official_input_price': 1, 'official_output_price': 2, 'modalities': {'input': ['text'], 'output': ['text']}})
        self.api(root, '/api/pricing/sell_prices', {'model_name': 'gpt-mock', 'channel_group': 'direct',
                 'input_per_mtok': 1, 'output_per_mtok': 2, 'active': True, 'source': 'manual'}, 'PUT')
        keys, shared, cases = [], [], [[] for _ in range(12)]
        for number in range(10):
            username, password = f'perf{number}', secrets.token_hex(20)
            self.api(root, '/api/user/', {'username': username, 'password': password, 'display_name': username})
            uid = int(self.sql(f"SELECT id FROM users WHERE username='{username}'"))
            # Schema 21 has no credit_seq. Install a synthetic opening movement
            # and the corresponding Core balance, without a modern bootstrap.
            self.sql(f"""WITH opening AS (
                SELECT {uid} AS id,100000000000-COALESCE(sum(delta_microusd),0) AS delta
                FROM wallet_ledgers WHERE user_id={uid}), updated AS (
                UPDATE users SET quota=100000000000 WHERE id={uid} RETURNING id)
                INSERT INTO wallet_ledgers
                (user_id,delta_microusd,kind,ref_type,ref_id,pricing_version,metadata,created_at)
                SELECT id,delta,'topup','benchmark_fixture','synthetic_initial_credit',0,'{{}}',NOW() FROM opening""")
            subprocess.check_output(['docker', 'exec', self.redis, 'redis-cli', 'DEL', f'user:{uid}'])
            headers = self.session(username, password)

            def token(name):
                created = self.api(headers, '/api/token/', {'name': name, 'expired_time': -1,
                                   'unlimited_quota': True, 'remain_quota': 0})
                if not isinstance(created, dict) or not isinstance(created.get('key'), str) or not created['key']:
                    raise RuntimeError('Schema-21 token creation did not return a credential')
                return created['key']

            keys.append(token('perf'))
            for index in range(12):
                cases[index].append(token(f'perf-case-{index}'))
            if number == 0:
                shared = [keys[0], *(token(f'perf-shared-{index}') for index in range(9))]
        for name, value in [('keys.txt', ','.join(keys)), ('single-key.txt', keys[0]),
                            ('same-project-keys.txt', ','.join(shared)), ('case-keys.json', json.dumps(cases))]:
            private_write(destination.parent / name, value)
        print('Legacy synthetic fixture: 10 payers; 10 keys share one project; 12 distinct case cohorts')


def main():
    Fixture(os.environ['LAZU_BENCH_GATEWAY_URL'], os.environ['LAZU_BENCH_UPSTREAM_URL'],
            os.environ['LAZU_BENCH_PG_CONTAINER'], os.environ['LAZU_BENCH_REDIS_CONTAINER']).create(sys.argv[1])


if __name__ == '__main__':
    main()
