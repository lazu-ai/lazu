#!/usr/bin/env python3
"""Check repository Markdown file links and public docs locale topic parity.

Site routes, external URLs and anchors are outside this filesystem check.
Fenced examples are skipped. Public page sources and generated/runtime text
use site URLs, so only maintainer documentation is checked for file links.
"""

import re
import subprocess
from pathlib import Path
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[2]
LINK = re.compile(r"\]\(\s*(?:<([^>]+)>|([^\s)]+))(?:\s+[^)]*)?\)")


def missing_links(path: Path) -> list[str]:
    errors = []
    fence = None
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        marker = re.match(r"^\s{0,3}(`{3,}|~{3,})", line)
        if marker:
            run = marker.group(1)
            if fence is None:
                fence = run
            elif run[0] == fence[0] and len(run) >= len(fence):
                fence = None
            continue
        if fence is not None:
            continue
        for match in LINK.finditer(line):
            target = match.group(1) or match.group(2)
            url = urlsplit(target)
            if url.scheme or url.netloc or not url.path or url.path.startswith("/"):
                continue
            if not (path.parent / unquote(url.path)).exists():
                errors.append(f"{path}:{number}: missing local target {target}")
    return errors


def main() -> int:
    files = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=ROOT,
    ).decode().split("\0")
    errors = []
    checked = 0
    for name in sorted(set(files)):
        path = ROOT / name
        if not name.endswith(".md") or not path.is_file():
            continue
        if not (
            "/" not in name
            or name.startswith(("docs/", "brand/", "ops/"))
            or name in ("apps/docs/README.md", "internal/controller/README.md")
        ):
            continue
        errors.extend(missing_links(path))
        checked += 1

    pages = ROOT / "apps/docs/pages"
    locales = ("zh", "zh-TW", "ja")
    expected = {
        str(path.relative_to(pages))
        for path in pages.rglob("*.mdx")
        if path.relative_to(pages).parts[0] not in locales
    }
    if not expected:
        errors.append("[docs] no English MDX topics found")
    for locale in locales:
        actual = {str(path.relative_to(pages / locale)) for path in (pages / locale).rglob("*.mdx")}
        if actual != expected:
            errors.append(
                f"[docs] {locale}: missing topics {sorted(expected - actual)}; "
                f"extra topics {sorted(actual - expected)}"
            )
    if errors:
        print("\n".join(errors))
        return 1
    print(f"[docs] local links checked in {checked} files; {len(expected)} topics aligned across 4 locales.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
