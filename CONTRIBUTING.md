# Contributing

## For contributors

Lazu is AGPL-3.0-or-later and based on
[new-api](https://github.com/QuantumNous/new-api). Contributions are welcome.

**Workflow**

1. Fork the repository and branch from `main`.
2. Start the stack with the README quick start, or run `./run.sh` for the
   local development loop. The gateway is Go; the console is Vite, React and
   TypeScript under `apps/web/`.
3. Commit messages follow Conventional Commits (`feat(scope): …`,
   `fix(scope): …`).
4. Before opening a pull request, `go build ./...` and `go test ./...` pass,
   and frontend changes pass `bun run typecheck` and `bun run eslint` in
   `apps/web`. The pull-request template lists the project's constraints.
5. Pull-request CI runs without repository secrets, so it works from forks.
   First-time contributors' workflows wait for a maintainer's approval.

**Sign your commits off (DCO)**

Every commit carries a `Signed-off-by:` line, added by `git commit -s`. It
certifies the [Developer Certificate of Origin](https://developercertificate.org/):
you wrote the change, or have the right to submit it under the project's
license. There is no contributor license agreement.

**Licensing of contributions**

Contributions are licensed under AGPL-3.0-or-later, the project license.
Do not add dependencies under licenses incompatible with it, and keep
`NOTICE`, the source copyright headers and the upstream attribution in the
console intact. Lazu cannot offer a commercial license: the upstream code it is
built on is not ours to relicense.

**Scope**

Small fixes can go straight to a pull request. For larger changes, open a
discussion or issue first; the product surface is kept deliberately small,
and pull requests that widen it without prior agreement may be declined.

The sections below are the project's engineering rules, in Chinese; they
apply to every contribution.

## 工程原则

lazu 接受代码会产生新债，但不接受债务不可见。技术债需要能被指出、能被量化、能排期清理。每季度做一次轻量 review，目标是保持 awareness，不是做审计式流程。

重构时遵守一个边界：换实现，不换行为。业务逻辑、国际化 key、用户可见文案、OpenAPI 合约和部署脚本不应被顺手改动。

## 配置原则

lazu 的默认配置面要保持克制。默认 Compose 文件和公开部署文档只说明启动服务必须理解、必须填写或长期稳定的产品级配置。

性能优化、恢复路径、灰度发布和内部回滚用的开关应优先保留在代码默认值里，必要时由维护者通过私有 env、Compose override、systemd env 或 Kubernetes Secret 显式覆盖。不要把这些开关放进默认 Compose 文件，除非它已经变成稳定、可解释、需要用户主动选择的产品能力。

钱相关或账务恢复相关的开关尤其要收紧。例如 `SETTLEMENT_OUTBOX_ENABLED` 这类开关应默认由代码持有安全默认值，并作为内部 rollout flag 使用；在完成压测、对账和恢复流程验证前，不应作为普通部署选项暴露给 self-host 用户。长期终态也应优先选择安全默认行为加隐藏的紧急关闭开关，而不是让用户在多个结算路径之间做选择。

## 前端规则

### Brand 单一来源

- `brand/` 是唯一设计与产品 UI 参考。正式约束只来自 `brand/BRAND.md`、`brand/foundations/*.md` 与 `brand/tokens/*`。
- 设计探索（`archive:brand/explorations/`，不在本仓库）仅保存选择历史；除非正式 foundation 明确引用已选方案，否则不得直接照搬到生产代码。
- `apps/web/src/components/ui/` 是 Brand 组件语言的 React 实现。页面组合 `Lazu UI`，不能自行定义第二套颜色、字体、圆角、阴影、动效或控件系统。
- 迁移期允许从 Brand token 生成兼容 alias，但 canonical value 必须单向来自 `brand/tokens/*`，不得由 `apps/web` 反向覆盖。
- Base UI 只提供无样式的可访问性、交互和状态机制，不是视觉规范。`@base-ui/react` 只能由 `apps/web/src/components/ui/` 导入，页面不得直接依赖 primitive。

### 做 UI 开发前查什么

| 改动类型                     | 必读 Brand 资料                                                                              | 实现入口                                               |
| ---------------------------- | -------------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| 任意产品 UI                  | `brand/BRAND.md`、`brand/foundations/yunmu-console.md`、`brand/foundations/quiet-precision.md` | 先查 `apps/web/src/components/ui/`                     |
| 新增或调整共享控件           | 上述资料 + `brand/foundations/ui-system.md`、`brand/foundations/component-contracts.md`      | 扩展 Lazu UI，再由页面组合                             |
| 颜色、字体、圆角、阴影、间距 | `brand/foundations/color-system.md`、`brand/tokens/*`                                        | 只消费 canonical token，不在页面定义新值               |
| 动效、弹层、拖拽或状态切换   | `brand/foundations/quiet-precision.md`、`brand/foundations/component-contracts.md`           | 遵守 120–180ms Direct Ease；空间变化上限 240ms；无弹跳 |
| 官网、营销页、文档呈现       | `brand/foundations/public-web-system.md`                                                     | 保持公共阅读节奏和单一主行动                           |
| Console、网关运营与数据界面  | `brand/foundations/gateway-product-system.md`                                                | 优先事实、影响和下一步行动                             |

`archive:brand/explorations/` 只能用于理解选择历史，不能作为生产实现依据。示例库（包括 Base UI、Animate UI 或 shadcn）只可借鉴交互结构；颜色、字体、圆角、阴影、密度和动效仍由 Lazu Brand 决定。

- 不 import `@douyinfe/semi-*` 或 `antd`。需要 UI 基础件时从 `apps/web/src/components/ui` 取。
- 不写 `any`，不加 `@ts-ignore`。类型不清楚时先补类型边界或收窄输入。
- 新前端代码使用 `.ts` / `.tsx`，并通过 `tsc --noEmit`。
- 新组件优先补到 `Lazu UI`，页面只组合组件，不在页面里复制一套控件样式。
- 提交信息使用 Conventional Commits，例如 `docs(api-reference): refresh OpenAPI overlays`、`chore(web): add tsconfig with path aliases`。

## 新 Lazu UI 组件流程

1. 先确认 `apps/web/src/components/ui` 是否已有等价组件。
2. 如果缺组件，在 `apps/web/src/components/ui` 下新增一个小而稳定的 primitive wrapper。
3. 需要无样式交互 primitive 时使用 Base UI；只在 Lazu UI 内导入，并用 Brand token、`class-variance-authority` 和已有 `cn()` helper 完成产品表达。
4. 从 `components/ui/index` 导出组件，保持页面 import 简洁。
5. 覆盖常用状态：默认、禁用、加载、错误、空状态或受控/非受控形态。
6. 不把业务数据请求、国际化 key 拼装或页面专属逻辑放进 Lazu UI。
7. 组合 Link 或自定义触发元素时使用 Base UI `render` 机制，不恢复 `asChild` 兼容 API。

## 本地检查

克隆后运行一次 `lefthook install`，提交前会自动跑 Prettier、ESLint 和类型检查（见 `lefthook.yml`）。

前端相关改动至少跑对应 app 的检查：

```bash
cd apps/web
bun run typecheck
bun run eslint
bun run test:unit
bun run build
```

Docs 相关改动：

```bash
cd apps/docs
npm run typecheck
npm run lint
npm run build
```

端到端检查使用 Playwright：

```bash
cd apps/web
bun run test:smoke
```

如果改动依赖后端或登录态，先用本地数据库里的测试账号确认流程，再补充截图或说明。

## 本地产物（local artifacts）

所有"只在本地有用、不该入 git"的产物统一放在仓库根的 `.local-results/`。整个目录已 `.gitignore`，禁止在仓库其他位置散落 `.redesign-review/`、`screenshots/`、`build-artifacts/` 这种平行目录。

子目录约定：

| 子目录                                  | 写入者                                      | 用途                                 |
| --------------------------------------- | ------------------------------------------- | ------------------------------------ |
| `.local-results/screenshots/`           | Playwright `warm-paper-*` spec、手动截图    | 视觉对比、设计 review                |
| `.local-results/screenshots/snapshots/` | 手动归档（用 `STAMP` 触发命名输出后挪过来） | 重要时刻的视觉基线                   |
| `.local-results/test-results/`          | Playwright（`outputDir`）                   | 失败 trace、失败截图                 |
| `.local-results/playwright-report/`     | Playwright HTML reporter                    | 本地 `npx playwright show-report` 用 |
| `.local-results/docs-build/`            | 手动跑文档构建/验证产生的副本               | 验证静态产物                         |
| `.local-results/lighthouse/`            | 预留                                        | 未来 Lighthouse 跑分                 |

### 截图的"过时策略"

默认每次 run 会写 `warm-paper-latest/`，**自动覆盖，不留历史**。这是好事：磁盘不会膨胀，永远反映"现在长啥样"。

需要做基线对比时（重构前留底、设计 review 留版本），用 `STAMP` 环境变量命名输出，再手动挪到 `snapshots/` 下：

```bash
# 改动前留底
STAMP=pre-vocs-migration bun run test:smoke
mv .local-results/screenshots/warm-paper-pre-vocs-migration \
   .local-results/screenshots/snapshots/pre-vocs-migration

# 改完看新版（无 STAMP，直接覆盖 latest）
bun run test:smoke

# 双开比对
open .local-results/screenshots/snapshots/pre-vocs-migration
open .local-results/screenshots/warm-paper-latest
```

`snapshots/<name>/` 下的目录**永久保留**，由你决定何时 `rm -rf` 清理。**不要按分支名、不要按时间戳自动归档**——会积累成无人能解释的目录树。
