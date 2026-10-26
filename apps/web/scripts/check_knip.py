#!/usr/bin/env python3
"""Fail when knip finds unused files, dependencies or exports, except where expected.

Reads `knip --reporter json` on stdin and turns it into one line per finding,
`<kind> <file>[ <name>]`, then compares the lines with two lists:

  knip-allow.txt     findings meant to stay, each with `  # <reason>` (an export
                     only an extension imports, an entry point knip cannot see).
  knip-baseline.txt  findings that existed when the check landed. It may only
                     shrink.

A finding in neither list fails. Listed findings that are no longer reported
are printed for the next change to delete; they do not fail.
"""
import json
import sys

KINDS = (
    'dependencies',
    'devDependencies',
    'optionalPeerDependencies',
    'unlisted',
    'binaries',
    'unresolved',
    'exports',
    'types',
    'duplicates',
)


def finding_lines(report):
    lines = {f'file {name}' for name in report.get('files', [])}
    for issue in report.get('issues', []):
        path = issue['file']
        for kind in KINDS:
            for item in issue.get(kind, []) or []:
                if isinstance(item, list):
                    name = '+'.join(sorted(entry['name'] for entry in item))
                else:
                    name = item['name']
                lines.add(f'{kind} {path} {name}')
        for enum, members in (issue.get('enumMembers') or {}).items():
            for member in members:
                lines.add(f'enumMembers {path} {enum}.{member["name"]}')
    return lines


def read_list(path, need_reason):
    entries, problems = set(), []
    with open(path, encoding='utf-8') as handle:
        for number, raw in enumerate(handle, 1):
            line = raw.rstrip('\n')
            if not line.strip() or line.lstrip().startswith('#'):
                continue
            finding, _, reason = line.partition('  #')
            if need_reason and not reason.strip():
                problems.append(f'{path}:{number}: no reason for {finding.strip()}')
            entries.add(finding.strip())
    return entries, problems


def main(argv):
    if len(argv) not in (3, 4):
        print('usage: check_knip.py <allow> <baseline> [--print] < knip.json', file=sys.stderr)
        return 2
    findings = finding_lines(json.load(sys.stdin))
    if argv[3:] == ['--print']:
        print('\n'.join(sorted(findings)))
        return 0
    allow, problems = read_list(argv[1], True)
    baseline, more = read_list(argv[2], False)
    problems += more
    new = sorted(findings - allow - baseline)
    gone = sorted((allow | baseline) - findings)
    for problem in problems:
        print(problem)
    if gone:
        print(f'{len(gone)} listed finding(s) are no longer reported; delete them from the lists:')
        for line in gone:
            print(f'  {line}')
    if new:
        print(f'{len(new)} new knip finding(s):')
        for line in new:
            print(f'  {line}')
        print('Delete the unused code or dependency. If it must stay, add the line to '
              f'{argv[1]} with "  # <reason>".')
    return 1 if new or problems else 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
