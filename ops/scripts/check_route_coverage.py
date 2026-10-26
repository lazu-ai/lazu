#!/usr/bin/env python3
"""Fail when the API serves a route nothing in this repository calls or
documents, unless ops/route-allowlist.txt names it with the kind of caller.

Reads `go run ./ops/tools/routes` output ("METHOD /path" per line) on stdin.
A route counts as covered when its path appears in the web app's source
(apps/web/src), the docs site (apps/docs/pages) or the OpenAPI reference
(packages/contracts/openapi). Path parameters match template expressions
(`${id}`), OpenAPI placeholders (`{id}`) or literal segments.

ops/route-allowlist.txt lines are "METHOD /path  # kind: reason", where kind
is one of: webhook, extension, external-api, browser, scheduled-removal
(with a date), or pending (not yet classified; the route table decides).
Allowlisted routes that are now covered or gone are listed for deletion and
do not fail the check.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
SOURCES = [
    (ROOT / 'apps/web/src', ('.ts', '.tsx', '.mjs', '.html')),
    (ROOT / 'apps/docs/pages', ('.md', '.mdx', '.ts', '.tsx')),
    (ROOT / 'packages/contracts/openapi', ('.yaml', '.yml', '.json')),
]
KINDS = ('webhook', 'extension', 'external-api', 'browser', 'scheduled-removal', 'pending')
PARAM = r'(?:\$\{[^}]*\}|\{[^}]*\}|:[A-Za-z_][A-Za-z0-9_]*|[A-Za-z0-9_.~%-]+)'


def corpus():
    parts = []
    for directory, suffixes in SOURCES:
        for path in sorted(directory.rglob('*')):
            if path.is_file() and path.suffix in suffixes and 'node_modules' not in path.parts and '.test.' not in path.name:
                parts.append(path.read_text(encoding='utf-8', errors='ignore'))
    return '\n'.join(parts)


def pattern(route_path):
    segments = []
    for segment in route_path.strip('/').split('/'):
        if segment.startswith(':'):
            segments.append(PARAM)
        elif segment.startswith('*'):
            segments.append(r'[^\s\'"`]*')
        else:
            segments.append(re.escape(segment))
    body = '/' + '/'.join(segments) if segments != [''] else '/'
    return re.compile(r'(?<![A-Za-z0-9_])' + body + r'/?(?=[\s\'"`?#)\]},;:]|\$\{|$)')


def read_allowlist(path):
    entries, problems = {}, []
    for number, raw in enumerate(path.read_text(encoding='utf-8').splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith('#'):
            continue
        route, _, note = line.partition('#')
        kind = note.strip().split(':', 1)[0].strip()
        if kind not in KINDS:
            problems.append(f'{path.name}:{number}: kind must be one of {", ".join(KINDS)}: {line}')
        if kind == 'scheduled-removal' and not re.search(r'\d{4}-\d{2}-\d{2}', note):
            problems.append(f'{path.name}:{number}: scheduled-removal needs a date: {line}')
        entries[' '.join(route.split())] = kind
    return entries, problems


def main(argv):
    allowlist_path = pathlib.Path(argv[1]) if len(argv) > 1 else ROOT / 'ops/route-allowlist.txt'
    routes = sorted({' '.join(line.split()) for line in sys.stdin if line.strip()})
    if not routes:
        # The enumerator always prints routes; no input means it failed.
        print('no routes on stdin; the route enumerator did not run', file=sys.stderr)
        return 2
    text = corpus()
    uncovered = [r for r in routes if not pattern(r.split(' ', 1)[1]).search(text)]
    if argv[2:] == ['--print']:
        print('\n'.join(uncovered))
        return 0
    allowed, problems = read_allowlist(allowlist_path)
    new = [r for r in uncovered if r not in allowed]
    stale = sorted(set(allowed) - set(uncovered))
    for problem in problems:
        print(problem)
    if stale:
        print(f'{len(stale)} allowlisted route(s) are now called, documented or gone; delete them from {allowlist_path.name}:')
        for route in stale:
            print(f'  {route}')
    if new:
        print(f'{len(new)} route(s) nothing in the repository calls or documents:')
        for route in new:
            print(f'  {route}')
        print(f'Call or document them, delete them, or add them to {allowlist_path.name} with their kind of caller.')
    return 1 if new or problems else 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
