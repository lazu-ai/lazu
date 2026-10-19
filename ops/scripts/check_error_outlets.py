#!/usr/bin/env python3
"""Guard the single error path.

Every failure leaves the server through the catalog (internal/types) and one
writer (internal/httperr, or the relay writers built on the same catalog).
The console reads failures by code (apps/web/src/lib/api-error.ts). This
script rejects the patterns that bypass that path:

Go (non-test files under internal/):
  - the retired writers: common.ApiError / ApiErrorMsg, writeControlPlaneError,
    writeOpenAIError, abortWithOpenAiMessage
  - hand-built failure JSON: `"success": false`
  - err.Error() written into a response
  - CJK text in errors.New / fmt.Errorf (errors are English; the console
    localizes by code)

Web (non-test files under apps/web/src):
  - throw new Error(<...>.message): rethrowing a server message drops its code
  - showError(<...>.message): same, at the toast
  - showServerError / translateServerMessage: retired helpers
  - importing sonner outside the toast owners

usage: check_error_outlets.py [path ...]   (defaults to the whole repo)
exit 0 when clean, 1 when a violation is found.
"""

from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
CJK = r"぀-ヿ一-鿿"

GO_RULES = [
    ("retired writer common.ApiError", re.compile(r"\bcommon\.ApiError(Msg)?\(")),
    ("retired writer writeControlPlaneError", re.compile(r"\bwriteControlPlaneError\(")),
    ("retired writer writeOpenAIError", re.compile(r"\bwriteOpenAIError\(")),
    ("retired writer abortWithOpenAiMessage", re.compile(r"\babortWithOpenAiMessage\(")),
    ("hand-built failure JSON", re.compile(r'"success"\s*:\s*false')),
    ("err.Error() written to a response", re.compile(r'"(message|error)"\s*:\s*[A-Za-z_]*[eE]rr\.Error\(\)')),
    ("CJK text in a Go error", re.compile(rf'\b(errors\.New|fmt\.Errorf)\([^)]*"[^"]*[{CJK}]')),
    ("CJK text in a response message", re.compile(rf'"message"\s*:\s*"[^"]*[{CJK}]')),
]
# Files that define the path itself, or protocols whose failure shape is not
# ours to change.
GO_ALLOW = {
    "internal/httperr/httperr.go",
}

WEB_RULES = [
    ("rethrowing a server message drops its code", re.compile(r"throw new Error\([^;]*\b(payload|res|response|data|body|result|json)\??\.(data\??\.)?message")),
    ("showError(x.message) drops the code", re.compile(r"showError\(\s*[A-Za-z_.?]*\.message\s*[,)]")),
    ("retired helper showServerError", re.compile(r"\bshowServerError\(")),
    ("reading success/message off a response", re.compile(r"\{\s*success\s*,\s*message\b|\{\s*message\s*,\s*success\b")),
    ("retired helper translateServerMessage", re.compile(r"\btranslateServerMessage\b")),
    ("import toasts from components/ui/toast", re.compile(r"^(?!import type ).*from ['\"]sonner['\"]")),
]
WEB_ALLOW = {
    "apps/web/src/components/ui/toast.tsx": {"import toasts from components/ui/toast"},
    "apps/web/src/helpers/utils.tsx": {"import toasts from components/ui/toast"},
    "apps/web/src/lib/query-client.tsx": {"import toasts from components/ui/toast"},
}


def strip_go_comment(line: str) -> str:
    in_string = False
    for i, ch in enumerate(line):
        if ch == '"' and (i == 0 or line[i - 1] != "\\"):
            in_string = not in_string
        if not in_string and line.startswith("//", i):
            return line[:i]
    return line


def scan(paths: list[pathlib.Path]) -> list[str]:
    problems: list[str] = []
    for path in paths:
        rel = path.relative_to(ROOT).as_posix()
        if rel.endswith("_test.go") or ".test." in rel or ".spec." in rel:
            continue
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except (UnicodeDecodeError, OSError):
            continue
        if rel.endswith(".go"):
            if rel in GO_ALLOW:
                continue
            rules = GO_RULES
            allowed: set[str] = set()
        else:
            rules = WEB_RULES
            allowed = WEB_ALLOW.get(rel, set())
        for number, raw in enumerate(lines, 1):
            line = strip_go_comment(raw) if rel.endswith(".go") else raw
            if rel.endswith(".go") and line.lstrip().startswith("//"):
                continue
            for name, pattern in rules:
                if name in allowed:
                    continue
                if pattern.search(line):
                    problems.append(f"{rel}:{number}: {name}\n    {raw.strip()}")
    return problems


def collect(args: list[str]) -> list[pathlib.Path]:
    roots = [ROOT / a for a in args] if args else [ROOT / "internal", ROOT / "apps/web/src"]
    files: list[pathlib.Path] = []
    for root in roots:
        if root.is_file():
            files.append(root)
            continue
        for suffix in ("*.go", "*.ts", "*.tsx"):
            for file in root.rglob(suffix):
                if "node_modules" in file.parts:
                    continue
                files.append(file)
    return sorted(set(files))


def main() -> int:
    problems = scan(collect(sys.argv[1:]))
    for problem in problems:
        print(problem)
    if problems:
        print(f"\n{len(problems)} error-path violation(s). See docs/ERRORS.md.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
