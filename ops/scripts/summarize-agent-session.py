#!/usr/bin/env python3
"""Reconcile an exported Agent Session without printing prompts or identifiers.

Usage: python3 ops/scripts/summarize-agent-session.py session.json
Only complete exports can produce a whole-session comparison.
"""
import argparse
from collections import defaultdict
from decimal import Decimal
import json
from pathlib import Path


def integer(value, field):
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError(f"{field} must be a non-negative integer")
    return value


def summarize(data):
    session = data["session"]
    requests = data["requests"]
    if not isinstance(requests, list):
        raise ValueError("requests must be an array")
    count = integer(session["request_count"], "request_count")
    total = integer(session["amount_microusd"], "session.amount_microusd")
    amounts = [integer(r["amount_microusd"], "request.amount_microusd") for r in requests]
    ids = [r["request_id"] for r in requests if r.get("request_id")]
    if len(set(ids)) != len(ids):
        raise ValueError("duplicate request IDs; re-export the session")
    complete = data.get("requests_complete") is True and len(requests) == count
    reconciled = complete and sum(amounts) == total
    by_model = defaultdict(lambda: {"requests": 0, "amount_microusd": 0})
    official = compared = eligible = 0
    for r, amount in zip(requests, amounts):
        row = by_model[r["model_name"]]
        row["requests"] += 1
        row["amount_microusd"] += amount
        anchor = integer(r.get("official_microusd", 0), "official_microusd")
        if not r.get("failed") and anchor > 0:
            official += anchor
            compared += amount
            eligible += 1
    result = {
        "complete": complete,
        "reconciled": reconciled,
        "session_requests": count,
        "loaded_requests": len(requests),
        "session_usd": str(Decimal(total) / 1_000_000),
        "loaded_usd": str(Decimal(sum(amounts)) / 1_000_000),
        "failed_requests": sum(bool(r.get("failed")) for r in requests),
        "failed_usd": str(Decimal(sum(a for r, a in zip(requests, amounts) if r.get("failed"))) / 1_000_000),
        "models": dict(by_model),
    }
    if reconciled and official > 0:
        result["comparable_subset"] = {
            "requests": eligible,
            "official_usd": str(Decimal(official) / 1_000_000),
            "charged_usd": str(Decimal(compared) / 1_000_000),
            "difference_usd": str(Decimal(official - compared) / 1_000_000),
            "difference_percent": str((Decimal(official - compared) * 100 / official).quantize(Decimal("0.01"))),
        }
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("export", type=Path)
    args = parser.parse_args()
    try:
        result = summarize(json.loads(args.export.read_text()))
    except (KeyError, ValueError, TypeError, OSError) as exc:
        parser.exit(2, f"Cannot reconcile export: {exc}\n")
    print(json.dumps(result, indent=2, ensure_ascii=False))
    if not result["reconciled"]:
        parser.exit(1, "Incomplete or mismatched details: no whole-session savings claim.\n")


if __name__ == "__main__":
    main()
