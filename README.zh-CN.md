<p align="center">
  <a href="https://lazu.ai">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="./brand/assets/logo/current/lazu-primary-paper.svg" />
      <source media="(prefers-color-scheme: light)" srcset="./brand/assets/logo/current/lazu-primary-graphite.svg" />
      <img alt="Lazu" src="./brand/assets/logo/current/lazu-primary-graphite.svg" height="44" />
    </picture>
  </a>
</p>

<h3 align="center">编程 Agent、多模态与自动化任务的模型账户。</h3>

<p align="center">
  把多平台账号收进一个开源网关：原生协议直连前沿模型，同时托管最新多模态与视频模型。
</p>

<p align="center">
  <a href="https://lazu.ai"><strong>lazu.ai</strong></a> ·
  <a href="https://lazu.ai/docs/zh">文档</a> ·
  <a href="https://lazu.ai/docs/zh/integrations/overview">接入指南</a> ·
  <a href="https://lazu.ai/docs/zh/compare/overview">网关对比</a> ·
  <a href="./LICENSE">AGPL-3.0</a> ·
  <a href="./README.md">English</a>
</p>

<p align="center">
  <a href="https://lazu.ai">
    <img src="./brand/assets/logo/social/lazu-github-social-1280x640.png" alt="Lazu — 编程 Agent、多模态与自动化任务的模型账户。" width="100%" />
  </a>
</p>

---

## 架构总览

```mermaid
flowchart LR
  subgraph Clients["编程 Agent 与自动化任务"]
    CC["Claude Code<br/>(/v1/messages)"]
    CX["Codex CLI<br/>(/v1/responses)"]
    OC["OpenCode / cc-connect / SDK<br/>(/v1/chat/completions)"]
    VID["视频与多模态任务<br/>(/v1/videos, /v1/images)"]
  end

  subgraph Lazu["Lazu 网关 (Go · AGPL-3.0)"]
    AUTH["Key 权限范围与<br/>硬预算预扣"]
    CODEC["无损协议<br/>编解码"]
    ROUTE["线路路由与<br/>缓存亲和"]
    LEDGER["单次调用用量与<br/>计费账本"]
    AUTH --> CODEC --> ROUTE --> LEDGER
  end

  subgraph Upstreams["上游供给"]
    ANT["Anthropic / OpenAI<br/>Gemini / DeepSeek"]
    MOD["托管开源多模态与<br/>视频模型"]
  end

  CC --> AUTH
  CX --> AUTH
  OC --> AUTH
  VID --> AUTH
  ROUTE --> ANT
  ROUTE --> MOD
```

## 核心能力

- **一个入口，四种原生协议。** 原生提供 OpenAI Chat Completions / Responses、Anthropic Messages 与 Gemini `generateContent`。跨协议仅在无损前提下桥接，不兼容字段明确返回 `400` 指出原因，绝不静默丢弃参数（[协议架构](./docs/protocol-conversion-architecture.md)）。
- **账户、Key 与硬预算。** 支持用户、团队、项目与按 Key 预算上限；请求前预扣、结束后严格按上游真实计数结算（[计费契约](./docs/billing-contract.md)）。
- **每笔请求可读账单。** 记录归一化 Token 与 Prompt Cache 维度、计费明细行与耗时，Key 所有者可按请求 ID 随时查回（[请求详情](https://lazu.ai/docs/zh/endpoints/usage-requests)）；编程 Agent 会话自动归组，支持离线脚本对账（[`summarize-agent-session.py`](./ops/scripts/summarize-agent-session.py)）。
- **可见路由与自动容灾。** 同一模型可挂多条上游线路，按优先级和权重调度，异常线路自动进入健康观察窗，并通过 `x-session-id` 保持多轮会话 Prompt Cache 命中。
- **扩展点而非 Fork。** 商业或定制部署通过 [docs/EXTENSIONS.md](./docs/EXTENSIONS.md) 的通用钩子编入，核心仓库保持独立可运行。

### 与普通薄代理的区别

| 常见痛点 | 普通薄代理 | Lazu |
| --- | --- | --- |
| 跨协议参数不兼容 | 静默丢弃 | 明确返回 `400` 指明不支持字段 |
| 余额或预算耗尽 | 客户端死循环重试 | 返回原生 `402` 立即停机 |
| 计费与缓存命中 | 黑盒扣额度 | 单次调用可查 Token / Cache 明细与账单行 |
| 客户端模型发现 | 手动抄写模型名 | 免 Key 公开 `GET /v1/models` 自动发现 |

## 生态与客户端接入

### 官方内置预设

| 项目 | 使用方式 | 接入指南 |
| --- | --- | --- |
| **[cc-connect](https://github.com/chenhg5/cc-connect)** | 已内置 Lazu 预设（覆盖 Claude Code、Codex `wire_api: "responses"`、OpenCode），运行 `cc-connect provider presets` 即可选用 | [cc-connect 指南](https://lazu.ai/docs/zh/integrations/cc-connect) |

### 主流编程 Agent 与客户端

| 客户端 | 协议 | 接入指南 |
| --- | --- | --- |
| **Claude Code** | 原生 Anthropic Messages（`https://api.lazu.ai`） | [Claude Code 指南](https://lazu.ai/docs/zh/integrations/claude-code) |
| **Codex CLI** | 原生 OpenAI Responses（`wire_api = "responses"`） | [Codex CLI 指南](https://lazu.ai/docs/zh/integrations/codex) |
| **OpenCode** | OpenAI 兼容（`https://api.lazu.ai/v1`） | [OpenCode 指南](https://lazu.ai/docs/zh/integrations/opencode) |
| **Goose** | 自定义 Provider JSON | [Goose 指南](https://lazu.ai/docs/zh/integrations/goose) |
| **Cursor、Cline、Cherry Studio、Chatbox、SDK** | OpenAI 兼容 + 公开 `GET /v1/models` | [接入总览](https://lazu.ai/docs/zh/integrations/overview) |

> **客户端或模型目录维护者**：`GET https://api.lazu.ai/v1/models` 与 `GET https://lazu.ai/api/pricing` 均公开免 Key，可直接用于实时模型与能力发现。

## 快速自托管（Docker）

```bash
cp .env.example .env        # 设置 ROOT_PASSWORD 与数据库密码
docker compose --env-file .env -f platform/docker/docker-compose.yml up --build -d
```

打开 <http://localhost:3000>，使用 `ROOT_USERNAME` / `ROOT_PASSWORD`（默认 `root` / `change-me-local-root`）登录。在 **模型运营** 添加上游渠道、创建 API Key 后，即可请求 `http://localhost:3001/v1/chat/completions`：

```bash
curl http://localhost:3001/v1/chat/completions \
  -H "Authorization: Bearer $LAZU_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"<目录中的模型 ID>","messages":[{"role":"user","content":"Hello"}]}'
```

完整部署清单与可选集成（邮件、支付、R2 存储）见 [docs/DEPLOYMENT.md](./docs/DEPLOYMENT.md)。

---

<details>
<summary><strong>开发者与本地调试说明</strong> — 仓库目录、<code>./run.sh</code>、文档构建与安全规范</summary>

- **快速启动**：`./run.sh`（复用现有依赖与缓存）；干净重建：`./rebuild.sh`；其他命令：`make help`。
- **本地端口**：PostgreSQL `127.0.0.1:10000`、Redis `127.0.0.1:10001`、API `3000`、Web `5173`、Docs `3002`。
- **文档开发**：`cd apps/docs && npm install && npm run dev -- --port 3002`。
- **安全与贡献**：请勿提交真实密钥；漏洞反馈见 [SECURITY.md](./SECURITY.md)，贡献规范见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

</details>
