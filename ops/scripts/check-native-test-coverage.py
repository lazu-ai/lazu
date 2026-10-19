#!/usr/bin/env python3
"""Require native financial fixtures to execute instead of passing by SKIP."""

import json
import pathlib
import sys

PREFIX = "github.com/lazu-ai/lazu/"
REQUIRED = {PREFIX + name for name in (
    "internal/ledger", "internal/model", "internal/controller", "internal/common",
    "apps/migrate", "internal/service/alerting",
)}

def main(path):
    seen = set()
    skipped = []
    failed = []
    counts = {package: 0 for package in REQUIRED}
    counts[PREFIX + "internal/service"] = 0
    for line in pathlib.Path(path).read_text().splitlines():
        item = json.loads(line)
        package, test = item.get("Package", ""), item.get("Test", "")
        if item.get("Action") == "fail":
            failed.append(f"{package}: {test or 'package'}")
        if item.get("Action") == "pass" and not test:
            seen.add(package)
        if item.get("Action") != "skip" or not test:
            continue
        financial_service = package == PREFIX + "internal/service" and any(
            word in test for word in ("Redis", "Ledger", "Identity", "Postgres", "LowBalance", "Budget")
        )
        if package in REQUIRED or financial_service:
            skipped.append(f"{package}: {test}")
            counts[package] = counts.get(package, 0) + 1
    for package, count in sorted(counts.items()):
        print(f"Native integration coverage {package}: SKIP={count}")
    missing = (REQUIRED | {PREFIX + "internal/service"}) - seen
    if failed or skipped or missing:
        raise SystemExit("Go tests failed or required native tests did not execute:\n" + "\n".join(failed + skipped + sorted(missing)))

if __name__ == "__main__":
    main(sys.argv[1])
