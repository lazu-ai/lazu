# ops/scripts

Repository checks run by CI or `make`, local stack helpers, and a manual smoke
kit for a running gateway.

## Checks and helpers

| Script | Run by |
| --- | --- |
| `check-brand-residue.sh` | CI (`ci-quality.yml`) |
| `check_error_outlets.py` | CI |
| `check-native-test-coverage.py`, `check_native_test_coverage_test.py` | CI |
| `secret_scan.py`, `test_secret_scan.py` | `make test-secrets` |
| `verify-doc-paths.sh`, `verify-doc-links.py` | `make test-docs` |
| `verify-relay-panics.sh` | `make test-relay-safety` |
| `stack.sh`, `dev.sh`, `lib/compose.sh` | `make stack-*`, `run.sh` |
| `summarize-agent-session.py`, `test_session_cost_summary.py` | by hand; the public docs page on agent session costs explains it |
| `audit-context-pricing.py` | by hand, during a pricing review |
| `files-api/` | by hand; see its README |
| `check-public-id-exposure.sh` | by hand |

## Manual smoke kit

`test.py`, `test_chat.py`, `test_claude.py`, `test_embeddings.py`,
`test_errors.py`, `test_gemini.py`, `test_gemini_tool_use.py`,
`test_long_prompt.py`, `test_max_tokens.py`, `test_responses.py` and
`image_generate.py` send one request to a running gateway and print what comes
back. They are not part of CI and are not pytest suites.

1. `pip install pyyaml requests`
2. Copy `config.example.yaml` to `config.local.yaml` (ignored by git) and fill
   in each profile's `api_base` and `api_key`. Never commit that file.
3. Run a script, for example:

   ```sh
   python3 ops/scripts/test_chat.py -p local -m <model> -s
   ```

   `-p` picks a profile from `config.local.yaml`, `-m` a model, `-s` streams.

The default model names inside the scripts drift as models are retired; pass
`-m` with a model the target gateway serves.
