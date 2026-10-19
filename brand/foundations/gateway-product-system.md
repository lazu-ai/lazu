# Gateway product system — role-aware API gateway

**Status: LOCKED**
**Locked: 2026-07-12**
**Decision provenance:** historical primary and secondary page selection boards removed after the rules below were locked

## Product boundary

Lazu 当前阶段是增强型 OpenAI-compatible API 网关。先完成账户、Workspace、API Key、模型、用量、预算、账单、渠道健康和平台运营的可靠迁移，再考虑智能路由产品化。

智能路由基础设施可以保留在服务端，但暂不进入主导航、核心销售叙事或客户日常任务。任何未来入口都必须作为可关闭的独立能力加入，不得破坏本文件定义的基础权限与数据边界。

## Role and scope model

Lazu 使用“两层客户租户 + 独立平台运营面”，不建立企业、部门、组、成员的任意深度组织树。

| Plane                 | Principal              | Visible scope                                | Allowed actions                                                                                                | Never visible                                                                  |
| --------------------- | ---------------------- | -------------------------------------------- | -------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Platform operations   | Lazu platform operator | All Workspaces, upstreams and infrastructure | Upstream channels, provider credentials, global model mapping, incidents, internal pricing and system settings | Customer API Key secrets after creation                                        |
| Customer Workspace    | Workspace manager      | One selected Workspace                       | Members, API Keys, model policy, budgets, Workspace usage, invoices and public Route status                    | Real upstream credentials, internal channel IDs and provider procurement cost  |
| Customer self-service | Member                 | Own account and owned Keys                   | Own usage, requests, approved models and policy-limited Key creation                                           | Other members, global billing and platform operations                          |
| Key Pass              | API Key viewer         | One verified API Key                         | Temporary read-only usage, requests, approved models and public Route status                                   | Full secret, mutations, other Keys and member identity beyond what is required |

The 2026-09-10 [team-projects foundation](team-projects-2026-09.md) refines the
customer plane into one owned default project plus invited memberships. Its
actor/target permissions, independent payer, service principal and shared-Key
reveal rules take precedence for that milestone. Platform account operations
remain a separate page and permission plane.

### Scope rules

1. Every usage, request, budget and API Key view declares its active scope.
2. A Workspace manager may see the whole Workspace; a member defaults to personal scope; Key Pass is restricted to one Key.
3. API Key verification creates a short-lived read-only session. The Key itself is not a console management credential.
4. Customer-facing Routes are stable service abstractions such as region and Route name. They may expose historical health and affected models, but not internal channel configuration.
5. Platform Operations is a separate permission plane even if one person holds both platform and Workspace identities.

## Locked primary page decisions

| #   | Decision                                  | Locked direction         | Implementation intent                                                                                                               |
| --- | ----------------------------------------- | ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| 01  | Console boundary and role switching       | **01 统一自适应控制台**  | One product shell adapts navigation and data to the active role and scope. Platform Operations remains an explicit protected scope. |
| 02  | Workspace overview                        | **01 成本与配额控制台**  | Lead with budget, spend, forecast and actionable quota risk rather than generic dashboard metrics.                                  |
| 03  | Members, roles and permissions            | **01 成员注册表**        | Searchable master-detail registry with role, owned Keys, spend, activity and access actions.                                        |
| 04  | API Key management                        | **01 策略注册表**        | Every Key binds an owner, environment, model policy, budget, network policy, lifecycle and rotation state.                          |
| 05  | Usage, requests and responsibility        | **02 Owner Drilldown**   | Begin with member or service ownership, then drill into Keys and request receipts for cost attribution.                             |
| 06  | Model catalog and public Route status     | **01 模型 + Route 状态** | Show capability, Workspace price, available public Routes and historical health together without exposing internal channels.        |
| 07  | Member and Key self-service               | **01 Key Pass 查询**     | Verify an API Key into a short-lived read-only session limited to that Key's usage and public service status.                       |
| 08  | Workspace budgets and billing             | **02 预算分配中心**      | Allocate Workspace budget to owners or Keys, show execution, forecast and intervention actions.                                     |
| 09  | Platform operations and upstream channels | **01 事故优先运营台**    | Lead with incidents, affected customers, fallback state and remediation; channel configuration is subordinate evidence.             |
| 10  | Create, invite and dangerous operations   | **02 分步 Dialog**       | Break creation and invitation into focused steps and show permission or customer impact before confirmation.                        |

## Locked secondary page decisions

| #   | Decision                                                  | Locked direction              | Implementation intent                                                                                                               |
| --- | --------------------------------------------------------- | ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| 01  | Workspace creation, list and switching                    | **01 Workspace 主从注册表**   | Use a searchable registry with role, members, spend and status; preserve a detail panel and explicit Scope switch.                  |
| 02  | Member detail and access management                       | **02 访问中心**               | Explain permission sources and effective access before exposing role, Key and security mutations.                                   |
| 03  | API Key detail, rotation and one-time Secret              | **01 安全状态详情**           | Put identity, owner, policy, budget, network restrictions, rotation state and recent evidence on one inspection surface.            |
| 04  | Workspace settings and defaults                           | **01 任务分组设置**           | Group settings by access, API Keys, budget, data and integrations instead of one flat configuration form.                           |
| 05  | Workspace model access policy                             | **01 Allowlist 注册表**       | Use an explicit model allowlist with price, public Route health, restrictions and default budget; do not imply intelligent routing. |
| 06  | Personal profile, security and sessions                   | **01 分区账户中心**           | Separate profile, authentication, sessions, notifications and dangerous account actions by risk.                                    |
| 07  | Notifications, Webhooks and status center                 | **01 事件矩阵**               | Configure event × channel × recipient visibly; mandatory security and payment facts cannot be disabled.                             |
| 08  | Audit and security events                                 | **03 字段 Diff 账本**         | Show actor, object, changed field, before, after and impact as immutable evidence distinct from request logs.                       |
| 09  | Payment methods, top-up and invoices                      | **03 发票账本**               | Keep invoices, payments, refunds and usage settlements in a traceable financial ledger.                                             |
| 10  | Platform customer Workspace management                    | **01 Workspace 注册表**       | Maintain a platform-only customer registry with plan, usage, risk, lifecycle and protected support actions.                         |
| 11  | Internal channel detail, mapping and credential lifecycle | **01 渠道检查器**             | Combine connection, health, model mappings, protected credentials and public Route impact for platform operators.                   |
| 12  | Developer Quickstart, testing and diagnostics             | **01 代码 + 回执 Quickstart** | Pair OpenAI-compatible integration code with a verifiable request receipt, public Route, latency and cost.                          |

## Composition consequences

1. The active Workspace or platform scope is always visible in console chrome.
2. Navigation is role-adaptive, not a hidden feature-flag copy of the same full sidebar.
3. Cost surfaces use Owner → API Key → Request as the primary drilldown path.
4. Customer status uses Model → public Route → historical health. Platform Operations may continue from Route to internal channel.
5. Secret material is one-time reveal or masked; exported and shared views are read-only and expire.
6. Creation flows may never grant permissions broader than the current Workspace policy.
7. Ordinary component states inherit `ui-system.md`; they do not require separate product-page decisions.

## Deferred capabilities

- Intelligent routing experiments, routing recommendations and policy optimization UI
- Arbitrary nested organizations or teams
- Customer access to raw upstream provider credentials or internal procurement economics
- Cross-Workspace consolidated control unless a concrete enterprise requirement justifies it
