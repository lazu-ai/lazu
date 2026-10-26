import io
import os
import sys
import tempfile
import unittest
from contextlib import redirect_stdout

sys.path.insert(0, os.path.dirname(__file__))
import check_deadcode  # noqa: E402


class CheckDeadcodeTest(unittest.TestCase):
    def run_check(self, surface, baseline, findings):
        with tempfile.TemporaryDirectory() as tmp:
            surface_path = os.path.join(tmp, 'surface.txt')
            baseline_path = os.path.join(tmp, 'baseline.txt')
            with open(surface_path, 'w') as handle:
                handle.write(surface)
            with open(baseline_path, 'w') as handle:
                handle.write(baseline)
            stdin, out = sys.stdin, io.StringIO()
            sys.stdin = io.StringIO(findings)
            try:
                with redirect_stdout(out):
                    code = check_deadcode.main(['x', surface_path, baseline_path])
            finally:
                sys.stdin = stdin
            return code, out.getvalue()

    def test_listed_findings_pass(self):
        code, _ = self.run_check('pkg.Hook  # extension hook\n', '# header\npkg.old\n', 'pkg.Hook\npkg.old\n')
        self.assertEqual(code, 0)

    def test_new_finding_fails(self):
        code, out = self.run_check('pkg.Hook  # extension hook\n', '', 'pkg.Hook\npkg.fresh\n')
        self.assertEqual(code, 1)
        self.assertIn('pkg.fresh', out)

    def test_surface_entry_needs_reason(self):
        code, out = self.run_check('pkg.Hook\n', '', 'pkg.Hook\n')
        self.assertEqual(code, 1)
        self.assertIn('has no reason', out)

    def test_gone_entries_are_listed_without_failing(self):
        code, out = self.run_check('pkg.Hook  # extension hook\n', 'pkg.removed\n', 'pkg.Hook\n')
        self.assertEqual(code, 0)
        self.assertIn('pkg.removed', out)


if __name__ == '__main__':
    unittest.main()
