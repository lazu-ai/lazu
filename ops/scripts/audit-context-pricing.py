#!/usr/bin/env python3
"""Create a review queue from a public /api/pricing JSON snapshot; never changes prices."""
import argparse
import csv
import json
import sys
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("snapshot", type=Path)
args = parser.parse_args()
payload = json.loads(args.snapshot.read_text())
writer = csv.DictWriter(sys.stdout, fieldnames=["model", "lane", "official_threshold", "lane_threshold", "supplier_policy", "receipt_evidence", "decision"])
writer.writeheader()
for model in payload["data"]:
    official = model.get("official_context_tier") or {}
    for lane in model.get("lanes", []):
        tier = lane.get("context_tier") or {}
        if not official and not tier:
            continue
        writer.writerow({
            "model": model["model_name"],
            "lane": lane.get("channel_group", lane.get("key", "")),
            "official_threshold": official.get("threshold", ""),
            "lane_threshold": tier.get("threshold", ""),
            "supplier_policy": "unverified",
            "receipt_evidence": "",
            "decision": "Verify supplier billing; do not copy official tiers automatically",
        })
