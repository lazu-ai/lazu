#!/usr/bin/env python3
"""Check that all shipped locale files expose the same usable key set."""

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


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--only", choices=["i18n"], default="i18n")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()

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
