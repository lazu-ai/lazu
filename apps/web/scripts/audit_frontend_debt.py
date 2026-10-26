#!/usr/bin/env python3
"""Check the shipped locale files.

--only i18n         every locale exposes the same usable key set.
--only i18n-unused  every key is read somewhere: by a literal, a template
                    with a known prefix (`page.x.${id}`, `${ns}.title` where
                    `const ns = 'page.x'`), a plural form of a read key, or
                    one of the lists below. Keys only an extension reads are
                    listed in src/i18n/extension-keys.txt; keys unused when
                    the check landed are listed in
                    src/i18n/unused-keys-baseline.txt, which may only shrink.
"""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
LOCALES = ROOT / "src" / "i18n" / "locales"
LOCALE_NAMES = ("en", "zh", "zh-TW", "ja")
CJK_RE = re.compile(r"[\u3400-\u9fff]")


def flatten(data: dict[str, Any], prefix: str = "") -> dict[str, str]:
    result: dict[str, str] = {}
    for key, value in data.items():
        full_key = f"{prefix}.{key}" if prefix else key
        if isinstance(value, dict):
            result.update(flatten(value, full_key))
        elif isinstance(value, str):
            result[full_key] = value
    return result


def load_locale(name: str) -> dict[str, str]:
    raw = json.loads((LOCALES / f"{name}.json").read_text(encoding="utf-8"))
    payload = raw.get("translation", raw) if isinstance(raw, dict) else {}
    if not isinstance(payload, dict):
        raise ValueError(f"{name}.json must contain a JSON object")
    feature = json.loads((ROOT / "src" / "features" / "projects" / "locales" / f"{name}.json").read_text(encoding="utf-8"))
    return {**flatten(payload), **flatten(feature)}


def audit() -> tuple[dict[str, Any], list[str]]:
    locales = {name: load_locale(name) for name in LOCALE_NAMES}
    reference = set(locales["en"])
    failures: list[str] = []
    details: dict[str, Any] = {
        "locale_names": list(LOCALE_NAMES),
        "key_counts": {name: len(values) for name, values in locales.items()},
        "missing_by_locale": {},
        "extra_by_locale": {},
        "empty_values_by_locale": {},
        "cjk_keys_by_locale": {},
    }

    for name, values in locales.items():
        keys = set(values)
        missing = sorted(reference - keys)
        extra = sorted(keys - reference)
        empty = sorted(key for key, value in values.items() if not value.strip())
        cjk_keys = sorted(key for key in keys if CJK_RE.search(key))
        details["missing_by_locale"][name] = missing
        details["extra_by_locale"][name] = extra
        details["empty_values_by_locale"][name] = empty
        details["cjk_keys_by_locale"][name] = cjk_keys
        if missing:
            failures.append(f"{name}: missing {len(missing)} keys")
        if extra:
            failures.append(f"{name}: has {len(extra)} keys not present in en")
        if empty:
            failures.append(f"{name}: has {len(empty)} empty translations")
        if cjk_keys:
            failures.append(f"{name}: has {len(cjk_keys)} untranslated CJK keys")

    return details, failures


SCAN_DIRS = ("src", "scripts", "tests", "index.html")
SCAN_SUFFIXES = (".ts", ".tsx", ".mjs", ".js", ".html")
EXTENSION_KEYS = ROOT / "src" / "i18n" / "extension-keys.txt"
UNUSED_BASELINE = ROOT / "src" / "i18n" / "unused-keys-baseline.txt"
CONST_RE = re.compile(r"\bconst\s+([A-Za-z_$][\w$]*)\s*=\s*['\"]([A-Za-z][A-Za-z0-9_.\-]*)['\"]")
DYNAMIC_RE = re.compile(r"`([A-Za-z0-9_.\-]+\.)\$\{")
TAIL_RE = re.compile(r"\}(\.[A-Za-z0-9_\-]+(?:\.[A-Za-z0-9_\-]+)*)(?=`|\$\{)")
INFIX_RE = re.compile(r"\}(\.[A-Za-z0-9_\-]+(?:\.[A-Za-z0-9_\-]+)*\.)\$\{")
PLURAL_RE = re.compile(r"_(zero|one|two|few|many|other)$")


def source_corpus() -> str:
    parts: list[str] = []
    for entry in SCAN_DIRS:
        path = ROOT / entry
        files = [path] if path.is_file() else sorted(path.rglob("*"))
        for file in files:
            if not file.is_file() or file.suffix not in SCAN_SUFFIXES:
                continue
            if "locales" in file.parts or "node_modules" in file.parts:
                continue
            text = file.read_text(encoding="utf-8", errors="ignore")
            # `${ns}.title` with `const ns = 'page.x'` in the same file reads
            # page.x.title; resolve the constant so the key is seen literally.
            for name, value in CONST_RE.findall(text):
                text = text.replace("${" + name + "}", value)
            parts.append(text)
    return "\n".join(parts)


def read_list(path: Path) -> set[str]:
    if not path.exists():
        return set()
    entries = set()
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.split("#", 1)[0].strip()
        if line:
            entries.add(line)
    return entries


def listed(key: str, entries: set[str]) -> bool:
    return key in entries or any(e.endswith(".*") and key.startswith(e[:-1]) for e in entries)


def unused_keys() -> tuple[list[str], list[str]]:
    corpus = source_corpus()
    dynamic = set(DYNAMIC_RE.findall(corpus))
    tails = set(TAIL_RE.findall(corpus))
    infixes = set(INFIX_RE.findall(corpus))

    def read(key: str) -> bool:
        if key in corpus or PLURAL_RE.sub("", key) in corpus:
            return True
        if any(key.startswith(prefix) for prefix in dynamic):
            return True
        if any(key.endswith(tail) and key != tail.lstrip(".") for tail in tails):
            return True
        return any(infix in key for infix in infixes)

    extension, baseline = read_list(EXTENSION_KEYS), read_list(UNUSED_BASELINE)
    keys = sorted(load_locale("en"))
    unread = [key for key in keys if not read(key) and not listed(key, extension)]
    new = [key for key in unread if key not in baseline]
    gone = sorted(entry for entry in baseline if entry not in set(unread))
    return new, gone


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--only", choices=["i18n", "i18n-unused"], default="i18n")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()

    if args.only == "i18n-unused":
        new, gone = unused_keys()
        if gone:
            print(f"{len(gone)} baseline key(s) are read or gone now; delete them from {UNUSED_BASELINE.name}:")
            for key in gone:
                print(f"  {key}")
        if new:
            print(f"{len(new)} translation key(s) nothing reads:")
            for key in new:
                print(f"  {key}")
            print("Delete them from every locale. If an extension reads them, list them in "
                  f"src/i18n/{EXTENSION_KEYS.name}.")
            return 1 if args.check else 0
        print("i18n unused-key check passed.")
        return 0

    report, failures = audit()
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        print(f"i18n locale key counts: {report['key_counts']}")

    if args.check and failures:
        for failure in failures:
            print(f"- {failure}")
        return 1
    if args.check:
        print("i18n locale parity check passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
