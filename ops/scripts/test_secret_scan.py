#!/usr/bin/env python3
"""Unit tests for the repository secret scanner."""

from __future__ import annotations

import unittest

from secret_scan import scan_text


class SecretScanTest(unittest.TestCase):
    def test_ellipsis_redacted_known_prefix_is_placeholder(self) -> None:
        findings = list(
            scan_text(
                "sample.env",
                "STRIPE_WEBHOOK_SECRET=whsec_abcd...wxyz\n",
            )
        )

        self.assertEqual([], findings)

    def test_ellipsis_in_line_does_not_suppress_real_secret(self) -> None:
        key = "sk-" + ("a" * 32)
        findings = list(
            scan_text(
                "sample.env",
                f"OPENAI_API_KEY={key} # docs show ... for omitted text\n",
            )
        )

        self.assertGreaterEqual(len(findings), 1)
        self.assertIn("openai api key", {finding.rule for finding in findings})

    def test_translated_copy_under_token_key_is_not_a_secret(self) -> None:
        findings = list(
            scan_text(
                "apps/docs/components/endpoint-billing.tsx",
                '    usageTokens: "按所选模型与线路的价格计算 token 用量；可能包含其他计费维度。",\n',  # secret-scan: allow -- scanner test input
            )
        )

        self.assertEqual([], findings)

    def test_marked_fixtures_in_test_files_are_skipped(self) -> None:
        fixture = 'cmd.Env = []string{"UPSTREAM_API_KEY=codex-probe-9Qm2Lx7Vb4"}\n'

        self.assertEqual([], list(scan_text("internal/agentsetup/agentsetup_test.go", fixture)))
        self.assertIn(
            "environment secret",
            {finding.rule for finding in scan_text("internal/agentsetup/agentsetup.go", fixture)},
        )

    def test_lazu_prefixed_value_not_shaped_like_an_issued_key_is_skipped(self) -> None:
        fixture = 'cmd.Env = []string{"LAZU_API_KEY=sk-lazu-from-env"}\n'

        self.assertEqual([], list(scan_text("internal/agentsetup/agentsetup_test.go", fixture)))

    def test_issued_lazu_key_is_caught(self) -> None:
        key = "sk-lazu-" + ("Q7xP" * 12)

        self.assertIn(
            "openai api key",
            {finding.rule for finding in scan_text("apps/web/src/demo.ts", f'const k = "{key}"\n')},
        )

    def test_unmarked_value_in_test_file_is_still_caught(self) -> None:
        fixture = "STRIPE_WEBHOOK_SECRET=whsec_9Qm2Lx7Vb4Nc8Rt1Zp6K\n"  # secret-scan: allow -- scanner test input

        self.assertIn(
            "environment secret",
            {finding.rule for finding in scan_text("internal/controller/stripe_test.go", fixture)},
        )

    def test_stripe_test_mode_key_in_test_file_is_still_caught(self) -> None:
        line = "STRIPE_SECRET_KEY=sk_test_51Qm2Lx7Vb4Nc8Rt1Zp6K\n"  # secret-scan: allow -- scanner test input

        self.assertIn(
            "environment secret",
            {finding.rule for finding in scan_text("internal/controller/stripe_test.go", line)},
        )

    def test_accented_password_is_still_caught(self) -> None:
        line = 'password: "mot-de-passe-Très-Sûr-2026!"\n'  # secret-scan: allow -- scanner test input

        self.assertIn("secret assignment", {finding.rule for finding in scan_text("config/app.yaml", line)})

    def test_cjk_password_in_config_is_still_caught(self) -> None:
        line = 'password: "correct horse 安全 battery staple 123!"\n'  # secret-scan: allow -- scanner test input

        self.assertIn("secret assignment", {finding.rule for finding in scan_text("config/backend/app.yaml", line)})

    def test_illustrative_lazu_key_in_mockup_is_skipped(self) -> None:
        line = 'krow("laptop","sk-lazu-972yK8dJm2QfXa1bL0pR3sT9uV6wX1Mg",sel=True)\n'

        self.assertEqual([], list(scan_text("brand/explorations/demo/gen.py", line)))

    def test_passphrase_with_spaces_is_still_caught(self) -> None:
        line = 'password: "correct horse battery staple 123!"\n'  # secret-scan: allow -- scanner test input

        self.assertIn("secret assignment", {finding.rule for finding in scan_text("config/app.yaml", line)})

    def test_provider_keys_are_still_caught_in_test_files(self) -> None:
        key = "sk-proj-" + ("Ab3" * 12)
        findings = list(scan_text("internal/relay/openai_test.go", f'const k = "{key}"\n'))

        self.assertIn("openai api key", {finding.rule for finding in findings})

    def test_private_key_block_is_still_caught(self) -> None:
        body = "\n".join(["MIIEow" + ("A" * 58)] * 3)
        text = f"-----BEGIN RSA PRIVATE KEY-----\n{body}\n-----END RSA PRIVATE KEY-----\n"

        self.assertIn("private key block", {finding.rule for finding in scan_text("deploy/key.pem", text)})


if __name__ == "__main__":
    unittest.main()
