import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("session_summary", ROOT / "ops/scripts/summarize-agent-session.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SessionCostSummaryTest(unittest.TestCase):
    def setUp(self):
        self.data = json.loads((ROOT / "ops/fixtures/agent-session.example.json").read_text())

    def test_comparison_uses_only_comparable_requests(self):
        result = module.summarize(self.data)
        self.assertTrue(result["reconciled"])
        self.assertEqual(result["session_usd"], "0.015")
        self.assertEqual(result["comparable_subset"]["difference_percent"], "27.50")
        self.assertEqual(result["comparable_subset"]["requests"], 2)

    def test_incomplete_or_mismatched_export_cannot_claim_savings(self):
        self.data["requests_complete"] = False
        self.assertNotIn("comparable_subset", module.summarize(self.data))
        self.data["requests_complete"] = True
        self.data["session"]["amount_microusd"] += 1
        self.assertFalse(module.summarize(self.data)["reconciled"])
        self.assertNotIn("comparable_subset", module.summarize(self.data))

    def test_duplicate_request_is_rejected(self):
        self.data["requests"][1]["request_id"] = "example-1"
        with self.assertRaises(ValueError):
            module.summarize(self.data)


if __name__ == "__main__":
    unittest.main()
