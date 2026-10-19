#!/usr/bin/env bash
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

python3 - "$@" <<'PY'
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

SELF = 'ops/scripts/check-brand-residue.sh'
LEGAL_NOTICES = {'NOTICE', 'THIRD_PARTY.md'}
COPYRIGHT_FILES = {
    'apps/web/src/pages/Home/components/LazuLanding.tsx',
    'apps/web/src/pages/Home/lazu-landing.css',
}
COPYRIGHT_HEADER = '''/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

For commercial licensing, please contact support@quantumnous.com
*/'''

NAMES = re.compile(r'dubrify|pandalla|quantumnous|songquanpeng|justsong', re.I)
# Identifier boundaries reject the former names, without matching substrings
# in unrelated identifiers such as renew_api or clone_api. Underscores can
# delimit a branded component of a longer identifier.
API_IDENTIFIERS = re.compile(r'(?<![a-z0-9])(?:one|new)[_.-]?api(?![a-z0-9])', re.I)
# Spaced prose (for example "one API call") is ambiguous, so it requires human
# review. A tracked path is a naming surface, and includes spaced variants.
SPACED_PATH_NAMES = re.compile(r'(?<![a-z0-9])(?:one|new) api(?![a-z0-9])', re.I)


# The upstream attribution AGPL and new-api's additional terms require: these
# exact links may appear anywhere, and nothing else naming the upstream may.
UPSTREAM_ATTRIBUTION = (
    '[new-api](https://github.com/QuantumNous/new-api)',
    '[one-api](https://github.com/songquanpeng/one-api)',
    'https://github.com/QuantumNous/new-api',
    'https://github.com/songquanpeng/one-api',
)


def without_attribution(line):
    for link in UPSTREAM_ATTRIBUTION:
        line = line.replace(link, '')
    return line


def forbidden(value, path=False):
    return bool(NAMES.search(value) or API_IDENTIFIERS.search(value) or
                (path and SPACED_PATH_NAMES.search(value)))


def scan(root):
    tracked = subprocess.check_output(['git', 'ls-files', '-z'], cwd=root)
    hits = []
    for raw in tracked.split(b'\0'):
        if not raw:
            continue
        name = os.fsdecode(raw)
        if forbidden(name, path=True):
            hits.append(f'{name}: forbidden tracked path')
        if name == SELF or name in LEGAL_NOTICES:
            continue
        file = root / name
        try:
            data = os.fsencode(os.readlink(file)) if file.is_symlink() else file.read_bytes()
        except FileNotFoundError:
            # A tracked deletion has no working-tree content to inspect.
            continue
        if b'\0' in data:
            continue
        text = data.decode('utf-8', errors='replace')
        if name in COPYRIGHT_FILES and text.startswith(COPYRIGHT_HEADER):
            # Preserve line numbers, exempt only the unchanged original header,
            # and still scan every other byte of these production source files.
            text = '\n' * COPYRIGHT_HEADER.count('\n') + text[len(COPYRIGHT_HEADER):]
        for number, line in enumerate(text.splitlines(), 1):
            if forbidden(without_attribution(line)):
                hits.append(f'{name}:{number}: {line.strip()}')
    return hits


def self_test():
    with tempfile.TemporaryDirectory(prefix='lazu-brand-scan-') as directory:
        root = Path(directory)
        subprocess.run(['git', 'init', '-q', directory], check=True)

        def stage(name, text):
            file = root / name
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text(text)
            subprocess.run(['git', 'add', '--', name], cwd=root, check=True)

        stage('fixture.txt', 'Lazu\n')
        assert not scan(root)
        stage('fixture.txt', 'Based on [new-api](https://github.com/QuantumNous/new-api).\n')
        assert not scan(root), 'the canonical attribution link is allowed'
        stage('fixture.txt', 'Based on new-api, see https://github.com/QuantumNous/new-api\n')
        assert scan(root), 'a bare upstream name beside the link is still residue'

        for value in ['DUBRIFY', 'Pandalla', 'oneAPI', 'one-api', 'one_api',
                      'one.api', 'newAPI', 'NEW_API', 'new.api', 'QuantumNous',
                      'songquanpeng', 'JustSong', 'NEW_API_URL']:
            stage('fixture.txt', value)
            assert scan(root), value
        for value in ['renew_api', 'clone_api', 'one API call', 'One API key',
                      'a new API endpoint', 'someoneapi', 'renewapi']:
            stage('fixture.txt', value)
            assert not scan(root), value
        stage('docs/new-api-migration.md', 'Neutral content\n')
        assert any('forbidden tracked path' in hit for hit in scan(root))
        subprocess.run(['git', 'rm', '-q', '-f', '--', 'docs/new-api-migration.md'], cwd=root, check=True)
        stage('docs/One API.md', 'Neutral content\n')
        assert any('forbidden tracked path' in hit for hit in scan(root))
        subprocess.run(['git', 'rm', '-q', '-f', '--', 'docs/One API.md'], cwd=root, check=True)
        stage('NOTICE', 'new-api by QuantumNous\n')
        assert not scan(root)
        for name in COPYRIGHT_FILES:
            stage(name, COPYRIGHT_HEADER + '\nLazu\n')
            assert not scan(root)
            stage(name, COPYRIGHT_HEADER + '\nnew-api\n')
            assert scan(root), 'legal header must not exempt the rest of a source file'
            stage(name, COPYRIGHT_HEADER.replace('2025', '2026'))
            assert scan(root), 'changed legal exceptions must be reviewed'
            stage(name, COPYRIGHT_HEADER)
    print('Brand scanner regression checks passed.')


try:
    if sys.argv[1:] == ['--self-test']:
        self_test()
    elif sys.argv[1:]:
        raise ValueError('usage: check-brand-residue.sh [--self-test]')
    else:
        hits = scan(Path.cwd())
        if hits:
            print('Legacy brand residue:\n' + '\n'.join(hits))
            sys.exit(1)
        print('Brand residue check passed (tracked paths and contents; legal notices retained).')
except (OSError, subprocess.CalledProcessError, ValueError) as error:
    print(f'Brand residue scan failed: {error}', file=sys.stderr)
    sys.exit(2)
PY
