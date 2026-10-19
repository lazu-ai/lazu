"""The CI evidence checker must never mask failures in any Go package."""

import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("native_coverage", Path(__file__).with_name("check-native-test-coverage.py"))
coverage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(coverage)

class NativeCoverageTest(unittest.TestCase):
    def check(self, extra):
        records = [{"Action": "pass", "Package": package} for package in coverage.REQUIRED | {coverage.PREFIX + "internal/service"}]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "tests.jsonl"
            path.write_text("\n".join(json.dumps(record) for record in records + extra))
            with contextlib.redirect_stdout(io.StringIO()):
                coverage.main(path)

    def test_native_passes(self):
        self.check([])

    def test_other_package_failure_is_rejected(self):
        with self.assertRaises(SystemExit):
            self.check([{"Action": "fail", "Package": coverage.PREFIX + "internal/router", "Test": "TestBroken"}])

    def test_financial_service_skip_is_rejected(self):
        with self.assertRaises(SystemExit):
            self.check([{"Action": "skip", "Package": coverage.PREFIX + "internal/service", "Test": "TestRedisLedgerAdmission"}])

    def test_unrelated_optional_font_skip_is_reported_separately(self):
        self.check([{"Action": "skip", "Package": coverage.PREFIX + "internal/service", "Test": "TestShareNotoCJK"}])

if __name__ == "__main__":
    unittest.main()
