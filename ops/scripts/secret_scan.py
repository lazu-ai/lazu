#!/usr/bin/env python3
"""Scan tracked repository files for high-confidence committed secrets."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable


ROOT = Path(__file__).resolve().parents[2]
MAX_FILE_BYTES = 2_000_000

SKIPPED_PATHS = {
    "apps/docs/package-lock.json",
    "apps/web/bun.lock",
    "go.sum",
    "package-lock.json",
    "pnpm-lock.yaml",
    "yarn.lock",
}
SKIPPED_PREFIXES = (
    ".local-results/",
    ".runtime/",
    "apps/web/dist/",
    "apps/web/node_modules/",
    "apps/web/test-results/",
    "node_modules/",
)
# Tests assign fixture credentials to *_KEY / secret variables by design. In a
# test file a value that names itself a fixture is not reported; any other value
# (a real whsec_/sk_test_ pasted into a test) still is. "test" is deliberately not
# a marker: every real Stripe test-mode key contains it.
TEST_FILE_PATTERN = re.compile(
    r"(?:_test\.go|\.(?:test|spec)\.[cm]?[jt]sx?|(?:^|/)test_[^/]+\.py)$"
    r"|(?:^|/)tests?/"
)
TEST_FIXTURE_MARKERS = ("fixture", "probe")
# CJK ideographs, kana, hangul and full-width punctuation mark translated copy,
# but only where copy lives: UI source, locale bundles and documentation.
# Config, env, Go and Python files report a CJK value like any other.
COPY_FILE_PATTERN = re.compile(r"\.(?:[cm]?[jt]sx?|json|mdx?)$")
CJK_TEXT = re.compile(r"[\u3000-\u30ff\u3400-\u9fff\uac00-\ud7af\uff00-\uffef]")
NAME_BASED_RULES = {
    "embedded url password",
    "environment secret",
    "redis password argument",
    "secret assignment",
}
ALLOWLIST_MARKERS = (
    "allowlist secret",
    "pragma: allowlist secret",
    "gitleaks:allow",
    "secret-scan: allow",
)
PLACEHOLDER_MARKERS = (
    "change-me",
    "changeme",
    "dummy",
    "example",
    "fake",
    "mock",
    "placeholder",
    "redacted",
    "replace",
    "sample",
    "todo",
    "your_",
    "your-",
    "your ",
)
PLACEHOLDER_PREFIX_VALUES = {
    "rk_live_",
    "rk_test_",
    "sk_live_",
    "sk_test_",
    "whsec_",
}
MAX_ELLIPSIS_PLACEHOLDER_REMAINDER = 8
# common.GenerateKey: "sk-lazu-" + 48 standard-base64 characters. A value with
# the prefix but not this shape (sk-lazu-from-env) cannot be an issued key.
LAZU_KEY_PREFIX = "sk-lazu-"
LAZU_KEY_SHAPE = re.compile(r"sk-lazu-[A-Za-z0-9+/]{48}")


@dataclass(frozen=True)
class Rule:
    name: str
    pattern: re.Pattern[str]
    secret_group: int = 0
    line_based: bool = True


@dataclass(frozen=True)
class Finding:
    path: str
    line: int
    rule: str
    preview: str


RULES = (
    Rule(
        "private key block",
        re.compile(
            r"-----BEGIN (?:RSA |DSA |EC |OPENSSH |PGP )?PRIVATE KEY-----"
            r"\s+[A-Za-z0-9+/=\r\n]{64,}\s+"
            r"-----END (?:RSA |DSA |EC |OPENSSH |PGP )?PRIVATE KEY-----",
            re.MULTILINE,
        ),
        line_based=False,
    ),
    Rule("aws access key id", re.compile(r"\b(?:A3T|AKIA|ASIA)[A-Z0-9]{16}\b")),
    Rule(
        "github token",
        re.compile(
            r"\b(?:gh[oprsu]_[A-Za-z0-9_]{36,255}|"
            r"github_pat_[A-Za-z0-9_]{22}_[A-Za-z0-9_]{59,255})\b"
        ),
    ),
    Rule("slack token", re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{20,}\b")),
    Rule("openai api key", re.compile(r"\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}\b")),
    Rule("stripe live key", re.compile(r"\b(?:sk|rk)_live_[A-Za-z0-9]{20,}\b")),
    Rule("google api key", re.compile(r"\bAIza[0-9A-Za-z_-]{35}\b")),
    Rule(
        "embedded url password",
        re.compile(
            r"\b(?:mysql|postgres|postgresql|redis)://[^:\s/@]+:"
            r"([^@\s'\"/]{8,})@"
        ),
        secret_group=1,
    ),
    Rule(
        "environment secret",
        re.compile(
            r"\b[A-Z0-9_]*(?:API_KEY|PASSWORD|SECRET|TOKEN)[A-Z0-9_]*="
            r"([A-Za-z0-9_./+=-]{8,})\b"
        ),
        secret_group=1,
    ),
    Rule(
        "redis password argument",
        re.compile(r"--(?:redis-password|requirepass)[\"']?,?\s*[\"']?([^\"'\s${}]{8,})"),
        secret_group=1,
    ),
    Rule(
        "secret assignment",
        re.compile(
            r"(?i)\b[A-Z0-9_.-]*(?:access[_-]?key[_-]?id|api[_-]?key|"
            r"apikey|auth[_-]?token|client[_-]?secret|password|passwd|"
            r"private[_-]?key|secret[_-]?access[_-]?key|secret[_-]?id|"
            r"secret[_-]?key|secret|token)[A-Z0-9_.-]*\b"
            r"\s*[:=]\s*['\"]([^'\"]{20,})['\"]"
        ),
        secret_group=1,
    ),
)


def git_ls_files() -> list[str]:
    result = subprocess.run(
        ["git", "-C", str(ROOT), "ls-files", "-z"],
        check=True,
        stdout=subprocess.PIPE,
    )
    return [item for item in result.stdout.decode().split("\0") if item]


def should_skip(path: str) -> bool:
    if path in SKIPPED_PATHS:
        return True
    return any(path.startswith(prefix) for prefix in SKIPPED_PREFIXES)


def read_text(path: Path) -> str | None:
    if path.stat().st_size > MAX_FILE_BYTES:
        return None
    data = path.read_bytes()
    if b"\0" in data:
        return None
    return data.decode("utf-8", errors="ignore")


def is_placeholder(value: str) -> bool:
    normalized = value.strip().lower()
    if not normalized:
        return True
    if value.strip().startswith(LAZU_KEY_PREFIX) and not LAZU_KEY_SHAPE.fullmatch(value.strip()):
        return True
    if normalized in PLACEHOLDER_PREFIX_VALUES:
        return True
    if "..." in normalized:
        without_ellipsis = normalized.replace("...", "")
        compact_without_ellipsis = re.sub(r"[^a-z0-9]+", "", without_ellipsis)
        for prefix in PLACEHOLDER_PREFIX_VALUES:
            if normalized.startswith(prefix):
                compact_prefix = re.sub(r"[^a-z0-9]+", "", prefix)
                if compact_without_ellipsis.startswith(compact_prefix):
                    compact_without_ellipsis = compact_without_ellipsis[
                        len(compact_prefix) :
                    ]
                break
        if len(compact_without_ellipsis) <= MAX_ELLIPSIS_PLACEHOLDER_REMAINDER:
            return True
    if "${" in normalized or "<" in normalized or ">" in normalized:
        return True
    if any(marker in normalized for marker in PLACEHOLDER_MARKERS):
        return True
    compact = re.sub(r"[^a-z0-9]+", "", normalized)
    if len(compact) <= 4:
        return True
    if re.fullmatch(r"[x*_ .-]+", normalized):
        return True
    return False


def is_translated_copy(path: str, value: str) -> bool:
    # A CJK sentence under a key named "token" or "secret" in a UI or docs file
    # is translated copy, not a credential. Spaces and accented letters alone
    # never rule a value out: passphrases have them.
    return bool(COPY_FILE_PATTERN.search(path) and CJK_TEXT.search(value))


def preview(value: str) -> str:
    clean = value.strip().replace("\t", " ")
    clean = re.sub(r"\s+", " ", clean)
    if len(clean) <= 12:
        return "<redacted>"
    return f"{clean[:4]}...{clean[-4:]}"


def line_number_for_offset(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def scan_text(path: str, text: str) -> Iterable[Finding]:
    is_test_file = bool(TEST_FILE_PATTERN.search(path))
    lines = text.splitlines()
    for line_number, line in enumerate(lines, start=1):
        lower_line = line.lower()
        if line.lstrip().startswith("#"):
            continue
        if any(marker in lower_line for marker in ALLOWLIST_MARKERS):
            continue
        for rule in RULES:
            if not rule.line_based:
                continue
            if rule.name in NAME_BASED_RULES and "${" in line:
                continue
            for match in rule.pattern.finditer(line):
                value = match.group(rule.secret_group) if rule.secret_group else match.group(0)
                if is_placeholder(value):
                    continue
                if rule.name in NAME_BASED_RULES:
                    if is_translated_copy(path, value):
                        continue
                    if is_test_file and any(
                        marker in value.lower() for marker in TEST_FIXTURE_MARKERS
                    ):
                        continue
                yield Finding(
                    path=path,
                    line=line_number,
                    rule=rule.name,
                    preview=preview(value),
                )

    for rule in RULES:
        if rule.line_based:
            continue
        for match in rule.pattern.finditer(text):
            value = match.group(rule.secret_group) if rule.secret_group else match.group(0)
            if is_placeholder(value):
                continue
            yield Finding(
                path=path,
                line=line_number_for_offset(text, match.start()),
                rule=rule.name,
                preview=preview(value),
            )


def scan_repo() -> list[Finding]:
    findings: list[Finding] = []
    for relative in git_ls_files():
        if should_skip(relative):
            continue
        absolute = ROOT / relative
        if not absolute.is_file():
            continue
        text = read_text(absolute)
        if text is None:
            continue
        findings.extend(scan_text(relative, text))
    return findings


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--json", action="store_true", help="print findings as JSON")
    args = parser.parse_args()

    findings = scan_repo()
    if args.json:
        print(json.dumps([finding.__dict__ for finding in findings], indent=2))
    elif findings:
        print("Secret scan failed. Review or allowlist these high-confidence findings:")
        for finding in findings:
            print(
                f"- {finding.path}:{finding.line} "
                f"[{finding.rule}] {finding.preview}"
            )
    else:
        print("Secret scan passed. No high-confidence committed secrets found.")

    return 1 if findings else 0


if __name__ == "__main__":
    raise SystemExit(main())
