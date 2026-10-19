"""Local-only harness locking and allowlisted environment evidence."""
import argparse
import contextlib
import fcntl
import json
import os
import pathlib
import platform
import re
import signal
import subprocess
import sys
from urllib.parse import urlsplit


def output_path(repo, requested):
    repo = pathlib.Path(repo).resolve()
    base = repo / '.local-results/gateway-bench'
    if base.resolve() != base:
        raise ValueError('Benchmark output base must not be a symlink')
    path = pathlib.Path(requested) if requested else base / 'latest'
    if not path.is_absolute():
        path = repo / path
    path = path.resolve()
    if path == base or not path.is_relative_to(base):
        raise ValueError('Benchmark output must be a child of repo/.local-results/gateway-bench')
    return path


@contextlib.contextmanager
def output_lock(path):
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor = os.open(path / '.run.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise ValueError('Benchmark output directory is already in use') from error
        yield descriptor
    finally:
        os.close(descriptor)
    # Keep the lock inode: unlinking it could give concurrent runs separate locks.


def run_locked(repo, requested, command):
    path = output_path(repo, requested)
    with output_lock(path) as descriptor:
        env = os.environ.copy()
        env['LAZU_BENCH_LOCKED_OUT_DIR'] = str(path)
        # The child retains the lock if the supervising Python process is killed.
        process = subprocess.Popen(command, env=env, pass_fds=(descriptor,))
        previous = {}
        def forward(signum, _frame):
            if process.poll() is None:
                process.send_signal(signum)
        try:
            for signum in (signal.SIGINT, signal.SIGTERM):
                previous[signum] = signal.signal(signum, forward)
            code = process.wait()
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)
        return code if code >= 0 else 128 - code


def capture(command):
    result = subprocess.run(command, text=True, capture_output=True, timeout=30)
    if result.returncode:
        # Docker stderr/context values can contain usernames or remote endpoints.
        raise ValueError('Local Docker environment inspection failed')
    return result.stdout.strip()


def require_unix(endpoint):
    parsed = urlsplit(endpoint)
    if (parsed.scheme != 'unix' or parsed.netloc or not parsed.path.startswith('/')
            or parsed.query or parsed.fragment):
        raise ValueError('Benchmark requires a local Unix Docker endpoint; remote endpoints are refused')
    return endpoint


def docker_endpoint(env=None, read=capture):
    env = os.environ if env is None else env
    override = env.get('DOCKER_HOST', '').strip()
    if override:
        require_unix(override)
    context = env.get('DOCKER_CONTEXT', '').strip()
    if not context and override:
        return override
    if not context:
        context = read(['docker', 'context', 'show'])
    endpoint = read(['docker', 'context', 'inspect', context,
                     '--format', '{{.Endpoints.docker.Host}}'])
    return require_unix(endpoint)


def host_facts():
    system = platform.system()
    cpu_model, memory = None, None
    if system == 'Linux':
        for line in pathlib.Path('/proc/cpuinfo').read_text().splitlines():
            if line.startswith(('model name', 'Hardware')):
                cpu_model = line.split(':', 1)[1].strip()
                break
        for line in pathlib.Path('/proc/meminfo').read_text().splitlines():
            if line.startswith('MemTotal:'):
                memory = int(line.split()[1]) * 1024
                break
    elif system == 'Darwin':
        memory = int(capture(['sysctl', '-n', 'hw.memsize']))
        cpu_model = capture(['sysctl', '-n', 'machdep.cpu.brand_string']) if platform.machine() == 'x86_64' else platform.machine()
    return {'os': system, 'release': platform.release(), 'architecture': platform.machine(),
            'cpu_model': cpu_model, 'logical_cpus': os.cpu_count(), 'memory_bytes': memory,
            'process_affinity_cpus': len(os.sched_getaffinity(0)) if hasattr(os, 'sched_getaffinity') else None}


RESOURCE_FIELDS = ('Memory', 'MemorySwap', 'MemoryReservation', 'NanoCpus', 'CpuQuota',
                   'CpuPeriod', 'CpuShares', 'CpusetCpus', 'CpusetMems', 'PidsLimit')


def manifest(pg_container, redis_container, read=capture):
    version = json.loads(read(['docker', 'version', '--format', '{{json .Server}}']))
    info = json.loads(read(['docker', 'info', '--format', '{{json .}}']))
    containers = {}
    for name, identifier in (('postgres', pg_container), ('redis', redis_container)):
        inspected = json.loads(read(['docker', 'inspect', '--format', '{{json .}}', identifier]))
        image = json.loads(read(['docker', 'image', 'inspect', '--format', '{{json .}}', inspected['Image']]))
        # Save only content digests, never registry/private endpoint names.
        digests = [value.rsplit('@', 1)[-1] for value in image.get('RepoDigests', [])
                   if re.fullmatch(r'sha256:[0-9a-f]{64}', value.rsplit('@', 1)[-1])]
        containers[name] = {'image_id': inspected['Image'], 'repo_content_digests': sorted(set(digests)),
                            'resources': {field: inspected['HostConfig'].get(field) for field in RESOURCE_FIELDS}}
    pg_settings = json.loads(read(['docker', 'exec', pg_container, 'psql', '-U', 'lazu', '-d', 'lazu', '-Atc',
        "SELECT json_object_agg(name,json_build_object('value',setting,'unit',unit))::text FROM pg_settings WHERE name IN ('max_connections','shared_buffers','work_mem','maintenance_work_mem','effective_cache_size','shared_preload_libraries','pg_stat_statements.track','track_commit_timestamp')"]))
    redis_settings = json.loads(read(['docker', 'exec', redis_container, 'redis-cli', '--json', 'CONFIG', 'GET',
                                     'maxmemory', 'maxmemory-policy', 'appendonly', 'appendfsync']))
    if isinstance(redis_settings, list):
        redis_settings = dict(zip(redis_settings[::2], redis_settings[1::2]))
    containers['postgres']['settings'] = pg_settings
    containers['redis']['settings'] = redis_settings
    return {'format': 'lazu-gateway-bench-environment-v1', 'host': host_facts(),
            'docker': {'endpoint_transport': 'unix',
                       'server': {key: version.get(key) for key in ('Version', 'ApiVersion', 'Os', 'Arch', 'KernelVersion')},
                       'resources': {key: info.get(key) for key in ('NCPU', 'MemTotal', 'CgroupDriver', 'CgroupVersion')}},
            'containers': containers,
            'scope': 'Host and Docker fixture resources; no environment dump, user, hostname or endpoint paths.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='action', required=True)
    run = commands.add_parser('run-locked')
    run.add_argument('--repo', required=True)
    run.add_argument('--out', default='')
    run.add_argument('command', nargs=argparse.REMAINDER)
    commands.add_parser('docker-endpoint')
    record = commands.add_parser('manifest')
    record.add_argument('--out', required=True)
    record.add_argument('--pg', required=True)
    record.add_argument('--redis', required=True)
    args = parser.parse_args()
    try:
        if args.action == 'run-locked':
            command = args.command[1:] if args.command[:1] == ['--'] else args.command
            if not command:
                raise ValueError('A benchmark child command is required')
            return run_locked(args.repo, args.out, command)
        if args.action == 'docker-endpoint':
            print(docker_endpoint())
        else:
            os.environ['DOCKER_HOST'] = docker_endpoint()
            os.environ.pop('DOCKER_CONTEXT', None)
            value = manifest(args.pg, args.redis)
            pathlib.Path(args.out).write_text(json.dumps(value, indent=2) + '\n')
        return 0
    except (ValueError, OSError, subprocess.TimeoutExpired) as error:
        print(f'Benchmark environment rejected: {error}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
