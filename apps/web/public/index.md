<!-- Lazu · Lazu — The model account for coding agents, video, and pipelines · https://lazu.ai/ -->

# The model account for coding agents, video, and pipelines.

Consolidate your model accounts into one open-source gateway — native APIs for frontier models, plus image and video models.

## One account, a key for every agent

- **No more signing up with every provider** — Claude, GPT, Gemini and DeepSeek all live in lazu. Fund it once and the whole company can use any of them — no separate accounts, top-ups or invoices.
- **Give everyone a cap that actually stops** — $50 for a new hire, $30 for a script. When the cap is reached the requests stop — you do not find out from next month's invoice.
- **One key per person, so a leak costs one key** — No shared company key. If someone's key ends up in a public repo, disable that one — everyone else keeps working.
- **A bill you can read and hand to finance** — Who, which model, when, how much — line by line. Group by person or model and export it as a spreadsheet.

## Three steps, running in ten minutes

1. **Sign up and fund** — Signing up creates a team account. Top up from $10; your first top-up gets 10% extra.
2. **Configure your gateway** — Choose the models and routes you want to use in the console.
3. **Create a key** — Paste the key into your tool and see its usage in the console.

## Works with the tools you already run

Native protocols with zero code rewrites and public /v1/models discovery — including a built-in preset in cc-connect.

- [cc-connect](https://lazu.ai/docs/integrations/cc-connect) (Built-in preset) — Control local Claude Code, Codex, and OpenCode from Telegram, Slack, or Lark — pick Lazu directly from presets.
- [Claude Code](https://lazu.ai/docs/integrations/claude-code) (Native Messages) — Preserves prompt caching and thinking blocks, stopping cleanly on HTTP 402 when a key's budget runs out.
- [Codex CLI](https://lazu.ai/docs/integrations/codex) (Native Responses) — Native wire_api = "responses" transport with full tool calling, reasoning effort, and cache affinity.
- [OpenCode](https://lazu.ai/docs/integrations/opencode) (OpenAI-compatible) — Point at api.lazu.ai/v1 to switch between Claude, GPT, Gemini, and DeepSeek on one key.
- [Goose](https://lazu.ai/docs/integrations/goose) (Custom JSON) — Drop in the provider-maintained custom JSON definition with automatic /v1/models discovery.
- [More clients & SDKs](https://lazu.ai/docs/integrations/overview) (Public /v1/models) — Cursor, Cline, Cherry Studio, Chatbox, and any OpenAI-compatible SDK work out of the box.

OpenAI-compatible endpoint: https://api.lazu.ai/v1 · Docs: https://lazu.ai/docs
