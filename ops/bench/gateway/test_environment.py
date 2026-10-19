"""Focused local tests; never contact Docker or start benchmark services."""
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import environment


class OutputTests(unittest.TestCase):
    def test_default_relative_and_absolute_paths(self):
        with tempfile.TemporaryDirectory() as folder:
            repo = pathlib.Path(folder).resolve()
            base = repo / '.local-results/gateway-bench'
            self.assertEqual(environment.output_path(repo, ''), base / 'latest')
            self.assertEqual(environment.output_path(repo, '.local-results/gateway-bench/server-0ms'), base / 'server-0ms')
            self.assertEqual(environment.output_path(repo, str(base / 'server-35ms')), base / 'server-35ms')

    def test_escape_root_and_symlink_paths_rejected_before_creation(self):
        with tempfile.TemporaryDirectory() as folder:
            repo = pathlib.Path(folder) / 'repo'
            repo.mkdir()
            base = repo / '.local-results/gateway-bench'
            for path in (base, repo / 'outside', repo / '.local-results/gateway-bench-other/run', base / '../../../escape'):
                with self.subTest(path=path), self.assertRaises(ValueError):
                    environment.output_path(repo, str(path))
            self.assertFalse(base.exists())
            base.mkdir(parents=True)
            (base / 'linked').symlink_to(pathlib.Path(folder), target_is_directory=True)
            with self.assertRaises(ValueError):
                environment.output_path(repo, str(base / 'linked/run'))
            (base / 'linked').unlink()
            base.rmdir()
            base.symlink_to(pathlib.Path(folder), target_is_directory=True)
            with self.assertRaises(ValueError):
                environment.output_path(repo, '')

    def test_output_lock_never_follows_lockfile_symlink(self):
        with tempfile.TemporaryDirectory() as folder:
            path = pathlib.Path(folder) / 'output'
            path.mkdir()
            secret = pathlib.Path(folder) / 'unrelated'
            secret.write_text('unchanged')
            (path / '.run.lock').symlink_to(secret)
            with self.assertRaises(OSError):
                with environment.output_lock(path):
                    self.fail('followed lockfile symlink')
            self.assertEqual(secret.read_text(), 'unchanged')

    def test_overlap_rejected_and_child_exit_releases_lock(self):
        with tempfile.TemporaryDirectory() as folder:
            repo = pathlib.Path(folder)
            output = repo / '.local-results/gateway-bench/owned'
            started, release = repo / 'started', repo / 'release'
            helper = pathlib.Path(environment.__file__).resolve()
            prefix = [sys.executable, str(helper), 'run-locked', '--repo', str(repo), '--out', str(output), '--']
            child = "import pathlib,time,sys; pathlib.Path(sys.argv[1]).write_text('first');\nwhile not pathlib.Path(sys.argv[2]).exists(): time.sleep(.01)\nsys.exit(7)"
            first = subprocess.Popen(prefix + [sys.executable, '-c', child, str(started), str(release)],
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                deadline = time.monotonic() + 5
                while not started.exists() and time.monotonic() < deadline and first.poll() is None:
                    time.sleep(.01)
                self.assertTrue(started.exists(), 'first lock holder did not start')
                second = subprocess.run(prefix + [sys.executable, '-c', "raise SystemExit('child must not execute')"],
                                        text=True, capture_output=True, timeout=5)
                self.assertEqual(second.returncode, 2)
                self.assertIn('already in use', second.stderr)
                self.assertNotIn('child must not execute', second.stderr)
                self.assertEqual(started.read_text(), 'first')
                release.touch()
                first.communicate(timeout=5)
                self.assertEqual(first.returncode, 7)
                third = subprocess.run(prefix + [sys.executable, '-c', 'pass'], timeout=5)
                self.assertEqual(third.returncode, 0)
            finally:
                release.touch()
                if first.poll() is None:
                    first.terminate()
                first.communicate(timeout=5)


class DockerTests(unittest.TestCase):
    def test_shell_entrypoint_rejects_remote_before_docker_or_build(self):
        repo = pathlib.Path(environment.__file__).resolve().parents[3]
        base = repo / '.local-results/gateway-bench'
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='environment-test-', dir=base) as folder:
            temporary = pathlib.Path(folder)
            fake_bin, invoked = temporary / 'bin', temporary / 'must-not-run'
            fake_bin.mkdir()
            for name in ('docker', 'go'):
                executable = fake_bin / name
                executable.write_text('#!/bin/sh\ntouch "$HARNESS_TEST_SENTINEL"\nexit 99\n')
                executable.chmod(0o700)
            env = os.environ.copy()
            env.update({'PATH': str(fake_bin) + os.pathsep + env['PATH'],
                        'DOCKER_HOST': 'ssh://private-user@private-host',
                        'LAZU_BENCH_OUT_DIR': str(temporary / 'output'),
                        'HARNESS_TEST_SENTINEL': str(invoked)})
            result = subprocess.run(['bash', str(repo / 'ops/bench/gateway/run.sh')],
                                    env=env, text=True, capture_output=True, timeout=5)
            self.assertEqual(result.returncode, 2)
            self.assertIn('Unix', result.stderr)
            self.assertNotIn('private-user', result.stderr)
            self.assertNotIn('private-host', result.stderr)
            self.assertFalse(invoked.exists())

    def test_remote_host_override_refused_before_any_docker_command(self):
        for endpoint in ('tcp://127.0.0.1:2375', 'tcp://remote:2376', 'ssh://user@remote', 'npipe:////./pipe/docker_engine'):
            with self.subTest(endpoint=endpoint):
                read = mock.Mock(side_effect=AssertionError('must not invoke Docker'))
                with self.assertRaisesRegex(ValueError, 'Unix'):
                    environment.docker_endpoint({'DOCKER_HOST': endpoint}, read)
                read.assert_not_called()

    def test_remote_current_or_explicit_context_refused_with_metadata_only(self):
        for env in ({}, {'DOCKER_CONTEXT': 'isolated'}, {'DOCKER_CONTEXT': 'isolated', 'DOCKER_HOST': 'unix:///example/docker.sock'}):
            calls = []
            def read(command):
                calls.append(command)
                return 'isolated' if command == ['docker', 'context', 'show'] else 'ssh://private@example'
            with self.subTest(env=env), self.assertRaisesRegex(ValueError, 'Unix'):
                environment.docker_endpoint(env, read)
            self.assertTrue(all(command[1] == 'context' for command in calls))

    def test_local_override_and_context_accepted(self):
        read = mock.Mock(side_effect=AssertionError('override needs no context command'))
        self.assertEqual(environment.docker_endpoint({'DOCKER_HOST': 'unix:///example/docker.sock'}, read), 'unix:///example/docker.sock')
        context = mock.Mock(side_effect=['desktop-linux', 'unix:///example/desktop.sock'])
        self.assertEqual(environment.docker_endpoint({}, context), 'unix:///example/desktop.sock')

    def test_manifest_contains_only_allowlisted_facts(self):
        image_id, content_digest = 'sha256:' + 'a' * 64, 'sha256:' + 'b' * 64
        def read(command):
            if command[1] == 'version':
                return json.dumps({'Version': 'test-version', 'Os': 'linux', 'Arch': 'amd64', 'private': 'private-user'})
            if command[1] == 'info':
                return json.dumps({'NCPU': 8, 'MemTotal': 16 * 1024**3, 'Name': 'private-host', 'HTTPProxy': 'private-endpoint'})
            if command[1] == 'inspect':
                return json.dumps({'Image': image_id, 'Name': 'private-user', 'Config': {'Env': ['SECRET=example-fixture-secret']},
                                   'HostConfig': {'Memory': 0, 'NanoCpus': 0, 'Binds': ['private-path']}})
            if command[1] == 'image':
                return json.dumps({'RepoDigests': ['private-registry/image@' + content_digest]})
            if command[3] == 'psql':
                return json.dumps({'max_connections': {'value': '30', 'unit': None}})
            if command[3] == 'redis-cli':
                return json.dumps(['maxmemory', '0', 'maxmemory-policy', 'noeviction', 'appendonly', 'yes', 'appendfsync', 'everysec'])
            raise AssertionError(command)
        with mock.patch.object(environment, 'host_facts', return_value={'os': 'Linux', 'logical_cpus': 8, 'memory_bytes': 16 * 1024**3}):
            value = environment.manifest('owned-pg', 'owned-redis', read)
        encoded = json.dumps(value)
        for secret in ('private-user', 'private-host', 'private-endpoint', 'example-fixture-secret', 'private-path', 'private-registry'):
            self.assertNotIn(secret, encoded)
        self.assertEqual(value['containers']['postgres']['image_id'], image_id)
        self.assertEqual(value['containers']['redis']['repo_content_digests'], [content_digest])
        self.assertEqual(value['containers']['redis']['resources']['Memory'], 0)
        self.assertEqual(value['containers']['postgres']['settings']['max_connections']['value'], '30')
        self.assertEqual(value['containers']['redis']['settings']['maxmemory'], '0')


if __name__ == '__main__':
    unittest.main()
