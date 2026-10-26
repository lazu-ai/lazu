#!/usr/bin/env python3
"""Fail when Go code nothing calls appears, except where it is expected.

Reads `deadcode -test` findings (one `<package path>.<function>` per line,
module prefix stripped) on stdin and compares them with two files:

  ops/extension-surface.txt  functions only an extension calls, each with
                             `  # <reason>`; a build without extensions sees
                             them as unreachable.
  ops/deadcode-baseline.txt  dead code that existed when the check landed.
                             It may only shrink.

A finding in neither file fails the check. Lines in either file that no longer
appear in the findings are listed so the next change can delete them; they do
not fail the check.
"""
import sys


def entries(path, need_reason):
    found, problems = set(), []
    with open(path, encoding='utf-8') as handle:
        for number, raw in enumerate(handle, 1):
            line = raw.rstrip('\n')
            if not line.strip() or line.lstrip().startswith('#'):
                continue
            name, _, reason = line.partition('#')
            name = name.strip()
            if need_reason and not reason.strip():
                problems.append(f'{path}:{number}: {name} has no reason')
            found.add(name)
    return found, problems


def main(argv):
    if len(argv) != 3:
        print('usage: check_deadcode.py <extension-surface.txt> <baseline.txt> < findings', file=sys.stderr)
        return 2
    surface, problems = entries(argv[1], need_reason=True)
    baseline, more = entries(argv[2], need_reason=False)
    problems += more
    findings = {line.strip() for line in sys.stdin if line.strip()}
    if not findings:
        # deadcode always reports something on this tree; no input means it
        # failed or never ran, which must not pass as a clean result.
        print('no deadcode output on stdin; the analyzer did not run', file=sys.stderr)
        return 2
    new = sorted(findings - surface - baseline)
    gone = sorted((surface | baseline) - findings)
    for problem in problems:
        print(problem)
    if gone:
        print(f'{len(gone)} listed function(s) are no longer reported; delete them from the lists:')
        for name in gone:
            print(f'  {name}')
    if new:
        print(f'{len(new)} function(s) nothing calls:')
        for name in new:
            print(f'  {name}')
        print('Delete the code. If an extension calls it, add it to '
              f'{argv[1]} with "  # <reason>" in the same change.')
    return 1 if new or problems else 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
