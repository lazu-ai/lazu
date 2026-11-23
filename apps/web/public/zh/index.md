<!-- Lazu · Lazu｜编程 Agent、多模态与自动化任务的模型账户：Claude、GPT、Gemini 一个 Key · https://lazu.ai/zh/ -->

# 编程 Agent、多模态与自动化任务的模型账户。

把多平台账号收进一个开源网关：原生协议直连前沿模型，同时提供图像与视频模型。

## 一个账户，每个 Agent 一把 Key

- **不用一家家开账号充值** — Claude、GPT、Gemini、DeepSeek 都在 lazu 里。充一次钱，全公司都能用，不用每家单独注册、单独充值、单独看账单。
- **给每个人一份额度，用完就停** — 新同事进来给他 $50，脚本给 $30。花到上限自动停，不会等到月底看账单才发现有人跑飞了。
- **一人一把 Key，出事只停一把** — 不用全公司共用一把 Key。谁的 Key 传到 GitHub 上了，停他那把就行，别人照常用。
- **账单能看懂，也能交给财务** — 谁、用了哪个模型、什么时候、花了多少，一条条列出来。可以按人或按模型汇总，导出成表格。

## 三步，十分钟内跑通

1. **注册并充值** — 注册就有一个团队账户。充 $10 起,第一次充值多送 10%。
2. **配置网关** — 在控制台选择要使用的模型和路由。
3. **创建 Key 开始用** — 把 Key 填到工具里即可调用，控制台会展示用量。

## 直接接入你正在用的工具

原生协议免改代码，公开 /v1/models 自动发现模型；已进入 cc-connect 官方内置预设。

- [cc-connect](https://lazu.ai/docs/zh/integrations/cc-connect) (内置预设) — 在飞书、钉钉、Telegram 里遥控本地 Agent，预设列表直接选择 Lazu。
- [Claude Code](https://lazu.ai/docs/zh/integrations/claude-code) (原生 Messages) — 保留 Prompt Cache 与思考块，预算触顶返回标准 402 立即停机。
- [Codex CLI](https://lazu.ai/docs/zh/integrations/codex) (原生 Responses) — 支持 wire_api = "responses"，完整保留工具调用与推理强度。
- [OpenCode](https://lazu.ai/docs/zh/integrations/opencode) (OpenAI 兼容) — 填入 api.lazu.ai/v1，一站切换 Claude、GPT、Gemini 与 DeepSeek。
- [Goose](https://lazu.ai/docs/zh/integrations/goose) (自定义 JSON) — 复制一份自定义 Provider JSON 即可导入，自动通过 /v1/models 拉取目录。
- [更多客户端与 SDK](https://lazu.ai/docs/zh/integrations/overview) (免 Key 发现) — Cursor、Cline、Cherry Studio、Chatbox 与任意 OpenAI SDK 通用接入。

OpenAI-compatible endpoint: https://api.lazu.ai/v1 · Docs: https://lazu.ai/docs
