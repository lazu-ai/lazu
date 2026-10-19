# Gateway / data-layer 本地验证 — 2026-10-03 至 2026-10-04

## PR #380 独立审查修复（基于 `8fabcef45`）

本轮修复用户确认的 17 项意见及 Redis/PostgreSQL CI 覆盖。逐条契约与测试名称见方案 B17.11（`lazu-ops:docs/plans/2026-10-03-gateway-performance-and-data-layer.md`）。本轮只使用隔离本地 PostgreSQL 16、Redis 7、合成历史数据与离线部署 mock（原有自启 Unix socket Redis helper 使用 Mac 宿主机的 redis-server），没有合并或部署；下面的 Linux 性能数字属于各自历史冻结源码，本轮没有重新发压，不能宣称修复后的源码仍获得同源 11/11。

仓库中的生产 Compose 配置现为 Redis `--maxmemory 1gb --maxmemory-policy noeviction --appendonly yes --appendfsync everysec`。独立 observer 对内存超过 70%、AOF 未启用、写入失败和重写失败发 Ops 告警；AOF 导致 Redis 无法写入去重键时，仍可通过进程内去重发送告警。1GB 为生产 4 核 / 7.9GB 主机留出 AOF 重写与 PostgreSQL 缓存空间；本轮没有执行该主机的内存压力或物理断电实验。

修复前复现与修复后证据分开保存于 `.local-results/pr380-review/{identity,migration,ledger,root}/`。关键失败包括预算 ProjectID=0、无关热 Key 在创建事务中遇到 pending、legacy 路由丢行/校验漏报、旧表 NULL 时间戳未回填、分区序列新 ID=1、永久 spool 挡住后续条目、320KB 响应被截为 256KiB、过期窗口延迟退款失败、绝对余额调账使用错基线、零余额付费请求放行、版本变化不重试和同库不同句柄通过检查。新增告警与工具同样保留未实现时的负例。

历史预扣分类工具只处理稳定关联及真实终态流水；旧版没有 request_id、纳秒 ref_id 互不关联且 preconsume metadata 为 0，很多真实历史行可能仍需人工证明。分类持久保存在 Core 的 `ledger_legacy_precharge_records`，包含规则版本、源哈希与选定财务证据，保留人工来源；不靠时间/金额相近推断结清，也不修改余额。命令、人工导入与 bootstrap 用法见 B17.11。

累计金额越界的隔离保留全部有效财务意图供人工处理，不代表取消。永久 spool 隔离不减少原金融 exposure，隔离目录仍计入库存、字节及已知未落库金额，未知金额独立列出；未分类隔离证据阻止恢复和 resume。Redis 与磁盘金额可能保守重复统计，后台一秒缓存用于准入保护。无法解析金额的库存会暂停新准入并告警，已接受结算及有效后续重放继续；这是未知财务敞口保护，不触发磁盘故障 latch。检测缓存仍保留最多约一秒的观察间隔。真实 I/O 故障仍触发财务停接单。消费邮件在后台 Core 提交后触发，保留原有尽力交付语义及 SQL 提交后崩溃可能漏发的边界，没有新增持久邮件 outbox。

CI 提供 PostgreSQL 16 双库、专用审计库、TCP/Unix socket 别名及 Redis 7 独立 logical DB，设置所有已有测试环境变量。`go test -p 1 -count=1 -json ./...` 配合 pipefail；覆盖检查器拒绝任何 Go 失败以及资金、迁移、身份相关 native SKIP。独立记录的真实上游 opt-in 与可选 CJK 字体测试不属于资金覆盖证明。完整本地验证命令（DSN/URL 仅放在隔离环境变量中）：

```bash
go test -p 1 -count=1 -json ./... > .local-results/pr380-review/root/go-full-final.jsonl
python3 ops/scripts/check-native-test-coverage.py .local-results/pr380-review/root/go-full-final.jsonl
go vet ./...
(cd apps/web && bun run typecheck)
python3 ops/scripts/deploy_schema_test.py   # private ops repository after the deploy switch
python3 ops/scripts/check_native_test_coverage_test.py
bash ops/scripts/check-brand-residue.sh
```

安全修补阶段 `45dfa2ceb` 的历史本地全量 `go test -p 1 -count=1 -json ./...` 退出 0（264.092s）：52 个包、2,501 个顶层测试通过；native 覆盖检查器通过，记账、身份、迁移、控制器及相关金融服务测试 SKIP=0。仅真实上游 opt-in `TestSingleUserTextChannelLive` 与缺本机 CJK 字体的 `TestStudioShareNotoCJK` 未运行，不作为资金覆盖证明。`go vet ./...`、Web typecheck、Docs typecheck/lint/build、6 个离线部署测试、4 个覆盖检查器测试、14 个秘密扫描器单测及完整扫描、Brand/文档链接/错误出口/relay guard/格式/Compose 校验均通过。新鉴权/完整捕获 service race 2.598s、共享 spool 隔离 race 11.758s、未知金额准入保护 race 3.253s 通过；本轮不重跑远端性能矩阵。该安全阶段的 JSON、覆盖及 race 日志保存在 `.local-results/pr380-review/root/` 和各专项目录；同步后的本轮结果单独记录，不能沿用这些历史统计。

首次远端修复提交 `57965b298` 的 backend 已通过；独立下载实际 CI 日志确认 TCP/Unix socket 回归执行、所有金融 native 范围 SKIP=0。随后安全扫描发现 goldmark 1.7.8 的可达 GO-2026-5320：五种实体编码/自动链接危险 URL 在真实 renderer 中先失败，再由升级 1.7.17 通过；安全扫描已确认零可达漏洞，完整 Go 与 vet 在补丁后重新通过。对应 `TestRenderMarkdownRejectsDangerousNormalizedURLs` / `TestRenderMarkdownPreservesSafeURLs`，来源见 [官方公告](https://pkg.go.dev/vuln/GO-2026-5320)。

Web audit 的 Axios 与 brace-expansion 升级至 1.20.0 / 5.0.11；[braces 公告](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm) 尚无上游修复版本，因此采用私有本地 `@lazu/braces@3.0.3-lazu.1`。原 tarball SHA512、各文件 SHA256、MIT 和可逆 PATCH 均保留。`vendor-braces.test.ts` 的 14 项回归覆盖深输入先失败、父/子循环、有界 AST、普通 glob/range、可逆上游证明及真实间接依赖安装文件逐字节一致性；干净 `bun install --frozen-lockfile` 和 `bun audit --audit-level=high` 通过，未增加公告忽略。Docker 安装前复制 vendor，Prettier 仅保留第三方原始源码格式，安全测试和 audit 正常执行。

安全修补阶段 `45dfa2ceb` 冻结补丁后的历史 Web 完整检查通过：typecheck、Brand、Prettier lint、ESLint、65 个文件 / 572 个单元测试、i18n、生产 build 和全部 size 预算。最终源码在全新空 node_modules 的独立目录中 frozen install 通过，真实 micromatch / Tailwind chokidar 的全部九个 runtime/LICENSE 文件与 vendor 逐字节一致；恶意深度及父循环均受控拒绝，普通 range 保持原行为。

另用实际 Web Dockerfile 在本地 Linux arm64 构建到 `build` target（82.98s）通过：Bun 1.3.14 frozen install、20 个 prerender、输出 guard、所有 bundle 预算均执行；网络关闭的镜像内验证实际间接依赖九个 runtime/LICENSE 文件与冻结源码一致，恶意输入拒绝及普通展开通过。仅生成并删除本任务测试 image，没有推送镜像或部署服务。

安全修补阶段 `45dfa2ceb` 的依赖在 esbuild 下初始 JS 为 281.13KiB，触发原 280KiB 门槛；使用 Vite 支持的 Terser 5.51.2 默认安全压缩后为 270.81KiB。全部体积预算通过，浏览器目标、407 个 manifest 节点及静态/动态依赖关系不变。生产 preview 的 CI `control-style-regressions.spec.ts` 11 项通过。补充 auth/loading/prerender 38 项为 32 PASS / 6 FAIL，六项在 esbuild 对照同样失败：auth skeleton 时序、`/models` preview 缺少 backend、三项旧 footer 自定义/链接断言、sitemap URL 列表与当前 sitemap index 断言差异；没有放宽断言，也不将这些失败算成通过。源码关系、体积和两种压缩产物的逐项结果保存在 `.local-results/pr380-review/identity/bundle-size-remediation.md` 及相邻日志。

生产数据副本上的全量迁移、故障恢复、备份恢复与旧版回退仍由 Claude 后续执行；本轮本地通过不构成这些操作已完成的证明。

## 主干同步 `2a89f2ef9` 后的本地集成证据

在 `45dfa2ceb` 安全阶段上同步 #383 / #384 / #385 后，保留 Core/Telemetry 分库、身份变更 fence、永久财务操作及 schema 25。本段只记录当前集成的实际局部结果；前述 **264.092s / 2,501**、**572 单元测试 / 270.81KiB / 407 节点**属于安全阶段历史，后面的 Linux 性能轮次也各有独立冻结源码。本轮没有再次发压，未沿用旧同源 11/11。

### schema 与 CoreDB 适配

新增 model 与 relay tests 使用 `CoreDB()` / `SetCoreDB`、`ConfiguredTelemetryDB()` / `SetTelemetryDB`，正确恢复 fixture 绑定并在关闭 stores 前等诊断写入排空。上游 #383 的观测检查与 #384 的 effort / tool-call 断言保留，未削弱。主干 schema 24 的 follow/ratio/pin 是附加列，不把本方案版本降为 24 或仅为本地旧 fixture 改为 26。

`.local-results/pr380-review/migration/main-sync-model-before.log` 是 incoming 代码引用已删除全局 `DB` 的实际编译 FAIL，修为 Core accessor。`main-sync-model-final.log` 六项 PASS **4.451s / SKIP=0**，包括：

- `TestInitDBLoadsPricingSnapshotForAPIProcess`
- `TestPostgresSchema24PriceUpgradePreservesFollowingFieldsAndMoney`
- `TestDeriveFollowingSellPriceScalesOfficialAndKeepsTheRest`
- `TestDeriveFollowingSellPriceKeepsPinnedFieldsAndUsesTheLaneRatio`
- `TestRecomputeFollowingSellPricesRepricesOnlyFollowingRows`
- `TestDeriveFollowingSellPriceKeepsTheOneHourWriteInStep`

真实 PG 测试从 literal schema 24 DDL 和三笔已有价格行、非零钱包及历史流水开始；运行真实迁移 24→25 及重复迁移后完整规范化行和余额相同，包含高精度 ratio、pin、fixed 和 inactive 历史价。先跑 SQLite API startup 也不会污染 PG 方言状态。显式 marker 23 保留物化价格与财务行；旧 23 程序没有 following-price 管理/重算功能。这里证明附加数据兼容，没有完成 B17.6 的整套停写、逐笔投影、遥测回拷及生产形状回退演练。`main-sync-model-fixture-setup-before.log` 的缺初始化错误是早期 test setup，不作为迁移丢钱缺陷证据。

### GPT-6 immutable / native wire

`.local-results/pr380-review/identity/gpt6-main-adaptation/before-undefined-db.log` 先保存新增 tests 的 store accessor 编译失败。`after-gpt6-tools-reasoning.log` 虽名为 after，但实际是编译恢复后的 **行为 FAIL**：native / bridge 默认值修改 caller semantic，且 native HTTP 体没有默认 effort。`before-native-reasoning-default.log` 独立复现 native 默认值缺失。

修后 `after-gpt6-path-fix.log` 规划 / attempt / native adapter / 实际 httptest HTTP relay 四包 PASS，`after-component-compatibility.log` 完整 textgen、runtime、OpenAI 组件 PASS。精确测试为 `TestPrepareAttemptTurnsReasoningOffForGPT6ChatTools`、`TestNativeGPT6ChatToolsReasoningDefaultPreservesCallerFields`、`TestChatToolsReachGPT6WithReasoningOff`。仅真正需要默认值时 clone，明确 high effort/reasoning、`tool_choice=none`、Responses 和 reseller 保留；重试从 caller 原始输入开始。native 测试还保留未知字段、大整数 9007199254740993、原始 escape 和输入 buffer。这里使用本地 SQLite / TCP httptest，不宣称真实供应商或 PostgreSQL 财务验证。

### following-price 精确财务边界

以下 8 个新增顶层测试 / 20 个子例均在最后同源专项中通过：

| 精确测试名                                                              | 已验证边界                                                                                                                                          |
| ----------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TestPR380FollowingPriceAliasPublishesSnapshotAndPreservesHeldVersion`  | Claude / Gemini catalog alias 重价立即发布新 SQL/Redis version；五次暖 quote 和 Redis reload 零 Core SQL；原 hold 使用旧金额/版本结算及真实 PG 入账 |
| `TestPR380FollowingPriceExplicitZeroAndUnknownRemainDistinct`           | scalar 明确零、group-only/缺失/非零存在性/manual 证据区分；免费 cache5m/1h、全部已知免费接受、全部未知拒绝                                          |
| `TestPR380OfficialPricePresenceRequiresAnObservedMatchingField`         | 只认可实际非 nil 接受/匹配字段；缺失、manual、锁定不匹配不证明零，微小正价匹配容差不变免费                                                          |
| `TestPR380OfficialPriceSameExplicitZeroImmediatelyRepricesFollowingRow` | 已存 catalog 数值零但本次首次得知确实免费，实际 reconcile 后立即物化零价/new version，并保留旧版本                                                  |
| `TestPR380FollowingPricePresentZeroContextTierPreservesFreeCache`       | tier pointer 零不变 nil、不回退正 base；缺失 tier 项仍回退 base                                                                                     |
| `TestPR380FollowingPriceAmbiguousAliasesRejectWithoutPublishing`        | 同规范 owner 不同事实拒绝整次重价，SQL 行数和原 Redis registry 不变                                                                                 |
| `TestPR380FollowingPriceAliasEquivalentUnitsAndTinyPrices`              | 2/8 与 .4/.013/.019 等价单位转换不误拒绝；极小 per-token 数值不重复乘百万                                                                           |
| `TestPR380FollowingPriceUnitEqualityRejectsInvalidAndDifferentFacts`    | finite 非零转换最多 2 ULP；不同价、零/正价、Inf/NaN 拒绝；MaxFloat64 不产生无限容差                                                                 |

修前有效证据均在 `.local-results/pr380-review/ledger/`：`following-price-before-valid-fixture.log`（alias 跳过、明确零仍收费、免费输入输出拒绝及歧义静默跳过）、`official-presence-before.log`（真实字段证据缺失）、`same-zero-trigger-before.log`（同数值零不触发新版本）、`tier-zero-before-valid-fixture.log`（tier 零回退）、`fractional-alias-before.log`（等价单位误拒绝）、`alias-unit-range-before.log`（极大有限值误判相等），均 exit 1。早期不完整 fixture 的 before 日志不冒称产品复现。

修后 `following-price-final-race.log` **PASS 12.900s / 12 顶层 / 13 子例 / SKIP=0**；原生隔离 PG16、Redis7 包含真实金融结算。`official-presence-final-race.log` **PASS 2.919s / 6 顶层 / 9 子例 / SKIP=0**；包含实际 reconcile 及既有 sync/context-tier 回归。只在控制面保存逐字段 `models.dev:zero` 证据，未增加 SQL 列或暖请求 SQL；不同 provenance/cache/tier 事实仍严格比较。完整 argv、修前/后分类与测试列表见 `following-price-evidence.md`。

### 当前 Web / Docs 与全量状态

`.local-results/pr380-review/identity/post-main-web/{report.md,results.json,metadata.json}` 记录最新九项 exit 0：typecheck、Brand、Prettier、ESLint、unit、i18n、build、size 和 CI control-style smoke。实际 **66 文件 / 576 单元测试**；四语言各 4,228 key 相同；生产 **20 prerender**、最新 manifest **406 节点**、初始 JS **270.77KiB / 280KiB**，全部体积预算通过。真实 production preview 的既有 CI `control-style-regressions.spec.ts` **11 项通过 / 零 retry**。407-node 完全相同关系证明与 Docker 构建属于此前安全阶段，不能套用到新增 ModelOps UI 的 406-node 图；依赖、锁文件、安全 fork、Terser 和预算未因本轮调整。

同步后的 Docs 实际运行 `npm run typecheck && npm run lint && npm run build` exit 0，build **14.026s / 84 页面加 404**。证据是工具 session 34560 的终端输出，没有 raw log 文件。最后 source freeze 后 `go test -p 1 -count=1 -json ./...` 已 exit 0：**282.094s / 52 包 / 2,519 顶层测试**通过；native 覆盖检查器确认迁移、身份、记账、控制器及相关金融服务 **SKIP=0**。仅真实上游 opt-in `TestSingleUserTextChannelLive` 与缺少本机 CJK 字体的 `TestStudioShareNotoCJK` 未运行。原始 JSON、统计和覆盖输出分别是 `.local-results/pr380-review/root/go-full-post-main.jsonl`、`go-full-post-main-summary.json` 与 `native-coverage-post-main.log`；`go vet ./...` 也已 exit 0（`go-vet-post-main.log`）。最终 CI 和生产副本迁移/恢复/回退没有在本段被宣称完成。

### 最终 CI 的取消错误链补修

quality run `37213654283` / head `d042fab18` 的实际失败仅为 `TestColdConcurrentAdmissionWaitsForSingleRebuildIntegration`：Redis dispatch 时到达deadline，错误被 `%v` 转成文字，丢失可识别的context cause。独立日志审计记录 **51包PASS / 2,517顶层PASS / 1测试FAIL＋账务包FAIL**，原17项38测试及schema/following/GPT-6都PASS，真实TCP/socket子例RUN/PASS。三个非资金SKIP是opt-in供应商、CJK字体及ffmpeg/libwebp；资金测试零SKIP，但Go失败使coverage summary、vet和部署mock步骤未执行，所以此轮不是完整资金gate成功。Web/Docs/security/secrets/changes与Docker全部PASS；失败证据在 `.local-results/pr380-review/migration/ci-backend-37213654283-failed-audit.md`。

最小补修保留 `ErrUnavailable`、既有continuity sentinel与原cause，不改等待helper、20ms原断言、重试时限或金额状态。新增精确测试为 `TestLedgerRedisDispatchPreservesCancellationChainIntegration`、`TestColdInitializePreservesSnapshotCancellationIntegration`、`TestLedgerContinuityAbsentAndMismatchKeepSentinelIntegration`。八个超时/取消dispatch边界先全部FAIL，修后 `-count=20` PASS **3.392s**；含原冷重建/全部mutation连续性/warm script cache的 `-race -count=3` PASS **5.643s**，零SKIP。钱包/Key余额、request、reservations、事件及初始化fence均检查不变。原始日志在 `.local-results/pr380-review/ledger/cold-cancellation-chain-{before,after-repeated,after-race}.log`。

补修源码冻结后的新鲜三库全量 `go test -p 1 -count=1 -json ./...` 已exit0：**283.302s / 52包 / 2,522顶层PASS**，native覆盖检查器确认七个相关包资金/身份/迁移 **SKIP=0**；`go vet ./...`也PASS。两个本机非资金opt-in跳过仍为供应商和CJK字体。实际原始/统计/覆盖证据为 `.local-results/pr380-review/root/go-full-deadline-final.jsonl`、`go-full-deadline-final-summary.json`、`native-coverage-deadline-final.log`及`go-vet-deadline-final.log`。本段记录本地结果，最终CI需补修提交推送后重新验收；`d042fab18` 的282.094s/2,519属于前一冻结源码。生产副本的迁移/恢复/R2/旧镜像回退仍由Claude执行。

实现依据为 完整方案（`lazu-ops:docs/plans/2026-10-03-gateway-performance-and-data-layer.md`），代码基于 `0182ad9c15195c5d55fd0b79da3c7cfb9beb55a5`。A/B 全部实施范围包括请求路径、Redis 准入与可恢复结算、Core/Telemetry 双库、迁移/备份/回退工具及 Ops 观测；生产数据没有迁移，本报告不构成合并或部署批准。

后台只读健康/对账已独立于财务 Drain，空 credit sequence 区间不查 SUM；局部 lifecycle/native Redis race 12.742s、真实 Core PG/native Redis race 15.889s 均通过。历史 Mac `final-awake` 对照见下文；睡眠中断的旧轮仅保留历史状态。

历史 Mac / OrbStack 同源 `final-awake` 对照：0ms **10/11**，35ms **10/11**。未通过门槛是 Responses 1KB/c1 TTFT ΔP50：**5.918 / 5.459ms >5ms**，原结果和门槛保留。随后新增 scanner 分配优化、Linux 环境清单及准确发压工具；该历史源码哈希不再代表当前源码。独立 8 核 / 16GB 新机的最终优化版同机 PostgreSQL、35ms 仿真 RTT 两轮现已分别 **11/11**，独特提示词、四组参数对照和 CPU 采样也已完成。各轮证据和未达到的高压目标分开记录。

结算成功等待 Redis Lua 回执，AOF 配置为 everysec；不等待逐笔磁盘 fsync。Redis 调用失败时 spool 完成文件与目录 fsync，二者不能混为断电 RPO0。方案 B5.9 已明确保留约一秒未刷盘风险（不是严格上界）；本轮财务对账与排空成功不替代物理断电/磁盘丢失恢复验收。

用户已选择保留两层身份快照严格 **60 秒**有效期；到期等权威重载遇到 SQL 不可用时返回 **503 `gateway_overloaded` / `Retry-After: 1`**。真实无效、撤销和额度拒绝保留原错误契约；会话在临时 SQL 故障中不会被删除。本地鉴权故障回归已通过，不替代 5 分钟数据库物理停机演练。

准入、A3/A11/B10/B7 及后台 observer 修正已随历史冻结源码完成对照；历史失败不改写为通过。scanner 优化后的scanner 阶段完整 Go / vet 日志为 `.local-results/gateway-optimization/go-all-scanner.log` 与 `go-vet-scanner.log`，均退出 0；helper / OpenAI adapter race 为 `scanner-race.log`，并发 128KiB 大帧及事件顺序回归通过。

## 独立 Linux 服务器实验

2026-10-04 使用用户提供的独立 8 核 / 16GB Ubuntu 主机，网关、worker、PostgreSQL 16、Redis 7、mock 上游与发压器同机。凭据、账户和数据库均为新生成的合成 fixture。环境清单记录实际主机资源、镜像 ID / 内容 digest、Docker 资源和数据库设置；数据库 max_connections=30、现代每库每角色连接池 6、GOGC=50、GOMEMLIMIT=384MiB。CPU admission monitor 与原矩阵一样关闭，权限、金融准入和费率保护保留，因此容量数值只适用于该合成配置。35ms 对照仅为 PostgreSQL TCP RTT 仿真，不代表真实 Neon 实例。

初轮 `server-native-0ms-postscanner` 完整 12 格短测及账务检查完成 **10/11**：Responses 1KB / 100KB / 600KB 单请求 TTFT ΔP50 为 **1.485 / 5.700 / 27.464ms**；所有短测暖 API SQL=0、required Redis dispatch=3。57,521 笔财务操作与流水数量相等，十个钱包的 Core / Redis / 流水余额相等，十项 drain 计数全零，Core 结算提交 P99 保守上界 1246ms。

唯一失败为实际发送速率：旧 `time.NewTicker(2ms)` 发压器在 Linux 上分别只发 28,019 / 27,775 个请求，即 **466.983 / 462.917 RPS**；已发请求全部完整 200。未将配置中的 500 RPS 当成实际吞吐，也未降低 >=495 RPS 原门槛。新工具使用原点起算的绝对 arrival 时间，记录 offered / dispatched / missed 及独立 schedule lag，窗口结束后不补发；默认十二种请求体字节和 SHA256 不变，TTFT 起点保持实际 HTTP 调用前。focused / race 通过，服务器重测另行记录。

初轮紧接的 35ms 启动遇到端口仍在回收，未产生 acceptance；这不是 35ms 性能失败或通过。顺序驱动现在等待测试端口释放，且不终止其他进程。上述初轮产物及失败记录均保留在 `.local-results/gateway-optimization/server-native-0ms-postscanner/` 和新机原始目录。

### 准确发压后的 Linux 基线

`server-native-paced-{0ms,35ms}` 两轮均退出 0，原有 **11/11** 数字检查通过；814 个运行时/工具文件、6 个二进制的哈希两轮一致。该快照包含 scanner 优化和绝对时间发压工具，尚不包含随后身份并发重载或 Responses 解码优化。两轮证据下载到 `.local-results/gateway-optimization/server-native-paced-{0ms,35ms}/`，不是当前交付源码无漂移的证明。

| 基线观察                                      |                同机 PG |       PG 额外 RTT 35ms |
| --------------------------------------------- | ---------------------: | ---------------------: |
| Responses 1KB / 100KB / 600KB c1 TTFT ΔP50 ms | 1.540 / 5.603 / 29.236 | 1.483 / 5.513 / 28.045 |
| Chat 1KB / 100KB / 600KB c1 TTFT ΔP50 ms      | 1.457 / 4.314 / 15.399 | 1.462 / 4.381 / 16.204 |
| 暖 API SQL / required Redis dispatch 每请求   |                  0 / 3 |                  0 / 3 |
| 单 Key 压力实际发送 RPS / 请求数              |       499.983 / 29,999 |       497.133 / 29,828 |
| 同项目十 Key 实际发送 RPS / 请求数            |       500.000 / 30,000 |       498.633 / 29,918 |
| 单 Key 压力 TTFT P95 / MAX ms                 |      339.538 / 438.128 |     337.020 / 3137.379 |
| 同项目十 Key 压力 TTFT P95 / MAX ms           |      347.289 / 510.116 |     341.303 / 2859.393 |
| Core 结算提交 P99 保守上界 ms                 |                   1185 |                   1625 |

所有已发压力请求完整 200，两轮钱包 Core / Redis / 永久流水相等、最终十项 drain 全零。**11 项通过不表示无长尾**：35ms 压力的少量首输出超过 2.8–3.1 秒，跨 60 秒身份有效期时分别出现 189 / 199 组身份 JOIN 与预算查询，存在同一 Key 并发权威重载的重复工作。保持严格 60 秒期限与 SQL 不可用 503 的前提下，单独修正并发重载，之后重新取得同源性能结果。

### 确切旧版的同机与 35ms 对照

旧版为 `6a29da9e75ab76af38d270b9be58151c94c3e63d`、schema 21；使用同机 Go 1.25.13 与现代基线的相同 mock / bench / pgdelay 二进制和请求体。两轮十二格短矩阵均观察到完整 200。该历史版本没有现代 ledger、阶段计时和必要 dispatch 指标，不能按现代 11 项检查计算通过率。旧 SQL 口径包含 API 进程内 consumer / 周期任务，与现代 API-only SQL 不能直接相减；连接池旧版为 15，现代为每库每角色 6。

| 协议 / KB / 并发    | 旧版 ΔP50 ms 同机 / 35ms | 新版基线 ΔP50 ms 同机 / 35ms |
| ------------------- | -----------------------: | ---------------------------: |
| Responses / 1 / 1   |        12.675 / 1750.767 |                1.540 / 1.483 |
| Responses / 100 / 1 |        54.855 / 1745.587 |                5.603 / 5.513 |
| Responses / 600 / 1 |       260.901 / 2651.385 |              29.236 / 28.045 |
| Responses / 600 / 8 |       634.980 / 2253.174 |              45.123 / 42.361 |
| Responses / 1 / 50  |        61.733 / 5412.989 |                8.926 / 8.631 |
| Chat / 1 / 1        |        21.773 / 2187.375 |                1.457 / 1.462 |
| Chat / 100 / 1      |        51.833 / 1854.685 |                4.314 / 4.381 |
| Chat / 600 / 1      |       193.497 / 1955.825 |              15.399 / 16.204 |
| Chat / 600 / 8      |       490.837 / 2142.838 |              27.060 / 29.114 |
| Chat / 1 / 50       |        77.977 / 5293.091 |                9.713 / 9.402 |

ΔP50 为各自 gateway P50 减 direct P50，分位数只包含完整成功响应，须同时查看样本/状态。每格运行 8 秒，旧版 35ms 单请求每格仅 2 个样本，不能把这些数值当作生产稳定分位数。旧版同机 Core 钱包/流水相等、旧队列排空；35ms Core 钱包/流水仍相等，但等待窗口结束时 relay 队列 pending=31、lag=494，**未排空**。完整数据与不支持指标在 `.local-results/gateway-optimization/server-legacy21-paced-{0ms,35ms}/`。

### 独特提示词与较高负载的基线观察

私有额外 harness 使用现代基线的五个业务/上游二进制原字节，单独发压器为相同源码的 Linux CGO=0 构建，明确记录各自 hash；每次将随机 nonce / worker / 序号放入实际提示词。`server-extra-baseline-unique-0ms` 十二格 unique 矩阵均完整 200；Responses 1/100/600KB c1 ΔP50 **1.531 / 6.670 / 30.997ms**，600KB/c8 **52.151ms**；Chat 对应 **1.851 / 4.748 / 19.055ms**、600KB/c8 **34.624ms**。这支持基线短测并非完全依赖相同提示词 memo；8 秒短样本不构成生产持续容量证明。

| unique 同项目十 Key 压力            |           目标 1000 RPS / 20s |            目标 2000 RPS / 20s |
| ----------------------------------- | ----------------------------: | -----------------------------: |
| 实际发送 / 实际 RPS                 |              12,174 / 608.700 |               10,182 / 509.100 |
| 未发 arrival                        |                         7,826 |                         29,818 |
| HTTP 完整状态                       |                   200: 12,174 |           200: 9,765；503: 417 |
| 发压 schedule lag P95 ms            |                      7642.559 |                      14375.532 |
| 成功响应 TTFT P50 / P95 / MAX ms    | 865.760 / 1011.286 / 2556.471 | 4517.033 / 6057.695 / 8597.339 |
| API CPU 1 秒采样最大值（100%=一核） |                        579.6% |                         689.2% |
| API RSS 采样最大 MiB                |                         405.5 |                          437.7 |

417 个 503 的 wire error 均为 `gateway_overloaded`；日志中观察到 Redis 连接池 timeout。API Redis pool=10、GOGC=50、GOMEMLIMIT=384MiB。CPU 最大值与 RSS 为离散采样，不是连续峰值；各进程最大值不同步，不能将其相加。网关、worker、mock 和发压器共享八核，发压 schedule lag 显著，**不把配置中的 1000/2000 RPS 当成已达，也不由此断言网关独立极限为 609 RPS**。

22,574 笔永久财务操作与流水数量相等，十钱包三方余额相等，financial pending/lag、reserved、exposure 和 spool 等均为零；但 `telemetry_dead=1`，90 秒排空结束仍不为零，额外 runner exit=1 / accounting.passed=false。该轮未在 fixture cleanup 前保存 dead payload/error，因此无法追认那一条的根因。源码独立复查确认早认证拒绝无日志目标却发布 post timing 的孤立补丁触发链，后续已修复并通过真实 PG / Redis 回归；这不追认历史那一条的根因。后续额外实验在清理前保留脱敏 DLQ 类型/错误/身份/字节哈希。财务一致不等于遥测无死信，原失败与资源证据保存在 `.local-results/gateway-optimization/server-extra-baseline-unique-0ms/`。

### 最终优化源码与功能检查

当前源码合并 scanner 复用、严格 common text 解码和同 Key 并发身份重载。`go-all-server-final.log` 全 Go 77.002s、`go-vet-server-final.log` 3.517s、`codec-native-server-final-race.log` 9.559s 均退出 0；middleware 23.563s race 通过。真实 PG / native Redis 的身份专项 race 12.823s，无 SKIP，32 个并发冷或到期请求仅一组 JOIN / 预算查询，60 秒期限、逐 waiter 新解码、变更 fence、取消与 SQL 失败 503 保留。

请求结束计时只在 Gin 本地已捕获或成功持久发布真实日志目标时排队，不新增暖路径 SQL / Redis；早期认证 503、公共接口及 `/models` 无目标时不发诊断。新 PG 媒体 active carrier 可以临时保存计时，正式 facts 保留计时并去 marker；过期/重放不能恢复。真实双 PG / native Redis race：model 15.148s、router 3.012s、service 2.007s；保留与过期重投递另 model 21.649s，均无 SKIP。日志在 `.local-results/gateway-optimization/diagnostic-target/`，源码清单 6 runtime + 4 tests。

最终优化 `server-native-optimized-{0ms,35ms}` 两轮均退出 0 / **11/11**；816 个运行时/工具文件、6 个二进制两轮完全相同，`server-final-pair-hashes.json` 已核对。所有暖短测 API SQL=0、required Redis dispatch=3、新连接比例=0；实际 gate 的 11 项均为 true，门槛和 TTFT 定义保持原值。

ΔP50 是 gateway 与 direct 两个独立样本组的首字 P50 之差；tail 是终止 SSE 事件至 EOF 的间隔。每格 8 秒，c1 只有 7 个完整成功样本；表中分位数不代表稳定生产分位数。

| API/KB/c        | 成功样本0/35 |  ΔP50 ms 0/35 | tail P95/MAX ms 0 | tail P95/MAX ms35 | API SQL0/35 | required Redis0/35 |
| --------------- | -----------: | ------------: | ----------------: | ----------------: | ----------: | -----------------: |
| responses/1/1   |          7/7 |   1.493/1.274 |       1.701/1.821 |       1.731/2.478 |         0/0 |        3.000/3.000 |
| responses/100/1 |          7/7 |   3.512/4.707 |       1.958/2.389 |       1.655/1.947 |         0/0 |        3.000/3.000 |
| responses/600/1 |          7/7 | 19.377/17.015 |       1.413/2.599 |       1.957/2.796 |         0/0 |        3.000/3.000 |
| responses/600/8 |        48/48 | 31.415/31.750 |       3.954/6.209 |       4.328/5.551 |         0/0 |        3.000/3.000 |
| responses/1/32  |      192/192 |   7.576/7.621 |       8.101/9.948 |       7.220/9.076 |         0/0 |        3.000/3.000 |
| responses/1/50  |      300/300 |   9.762/9.387 |     10.990/12.473 |     12.504/15.333 |         0/0 |        3.000/3.000 |
| chat/1/1        |          7/7 |   1.375/1.464 |       1.193/1.523 |       2.665/2.922 |         0/0 |        3.000/3.000 |
| chat/100/1      |          7/7 |   4.664/3.574 |       1.199/1.613 |       2.032/2.982 |         0/0 |        3.000/3.000 |
| chat/600/1      |          7/7 | 16.212/15.952 |       1.283/1.434 |       1.540/4.353 |         0/0 |        3.000/3.000 |
| chat/600/8      |        48/48 | 32.287/29.562 |       2.853/5.069 |       3.614/5.169 |         0/0 |        3.000/3.000 |
| chat/1/32       |      192/192 |   6.198/6.051 |       5.678/6.289 |       6.363/7.323 |         0/0 |        3.000/3.000 |
| chat/1/50       |      300/300 | 10.738/10.403 |      9.061/14.294 |      9.468/12.065 |         0/0 |        3.000/3.000 |

| 最终压力 / 账务              |              同机 PG |               PG RTT35ms |
| ---------------------------- | -------------------: | -----------------------: |
| 单 Key实际请求 / RPS / HTTP  | 30,000 / 500 / 全200 | 29,999 / 499.983 / 全200 |
| 十 Key实际请求 / RPS / HTTP  | 30,000 / 500 / 全200 |     30,000 / 500 / 全200 |
| 单 Key / 十 Key API SQL总数  |               3 / 20 |                   3 / 20 |
| 单 Key / 十 Key身份JOIN次数  |               1 / 10 |                   1 / 10 |
| 单 Key / 十 Key首字P95 ms    |    340.071 / 342.966 |        337.591 / 339.494 |
| 单 Key / 十 Key首字MAX ms    |    423.618 / 454.415 |        616.345 / 757.042 |
| Core提交P99 / MAX保守上界 ms |          1183 / 1336 |              1516 / 1799 |
| 永久操作 / 流水              |      61,736 / 61,736 |          61,735 / 61,735 |
| 钱包三方 / 最终十项drain     |         全相等 / 全0 |             全相等 / 全0 |

相较同机基线，Responses100/600KB c1开销分别降低37.3%/33.7%，600KB c8降低30.4%；Chat600KB c8反升19.3%、两协议1KB c50也稍增，所有格子均保留，8秒样本不足以归因。35ms单Key/十Key身份JOIN由189/199降到1/10，首字MAX由3137.379/2859.393ms降到616.345/757.042ms；TTL保持严格60秒，不用放宽权限换性能。四个15秒窗P95分别为：0ms单Key339.781/344.374/339.588/337.935、十Key354.468/338.799/338.691/340.154ms；35ms单Key331.172/335.468/337.386/353.976、十Key336.259/339.019/338.962/343.510ms。不把MAX改善当成所有分位数等比改善。Core提交不是完整Telemetry/Console可见性。unique及1000/2000RPS参数实验为额外观察，不属于这11项门槛。

该轮记录 816 个运行时/工具文件，业务运行时代码与现代实验实际执行工具均与交付源码相等；一处未执行的 Python 环境测试仅将合成敏感值改名为明确 example 占位符，hash 差异显式保留。源码包未包含与网关/worker构建无关的 `ops/tools/errdocs/main.go`；该已有工具在本地当前源码执行 `-check` 已退出 0。不能因此声称整仓所有文件无差异。历史 Linux 基线和历史 Mac 结果不代表这一版源码。

### 最终独特提示词与四组参数实验

四轮均使用最终 0ms 业务/上游二进制原字节和同一 unique 发压器；随机 nonce、worker、序号进入实际提示词。`server-extra-optimized-unique-0ms` 先做十二格 unique 矩阵；pool32、pool64、pool64+GC100/1GiB 三轮只做压力参数对照，没有重跑十二格。仅 API 的 Redis pool / Go 参数变化，worker 保持 pool10 / GOGC50 / 384MiB，SQL 池与准入配置不变；CPU admission monitor 仍关闭。

| unique API / KB / 并发 | 基线 ΔP50 ms | 最终默认 ΔP50 ms | 最终成功样本 |
| ---------------------- | -----------: | ---------------: | -----------: |
| responses / 1 / 1      |        1.531 |            1.940 |            7 |
| responses / 1 / 8      |        3.439 |            3.062 |           56 |
| responses / 100 / 1    |        6.670 |            4.962 |            7 |
| responses / 100 / 8    |       12.121 |            9.390 |           48 |
| responses / 600 / 1    |       30.997 |           22.586 |            6 |
| responses / 600 / 8    |       52.151 |           32.991 |           48 |
| chat / 1 / 1           |        1.851 |            1.482 |            7 |
| chat / 1 / 8           |        3.057 |            3.830 |           56 |
| chat / 100 / 1         |        4.748 |            4.729 |            7 |
| chat / 100 / 8         |        9.942 |            8.604 |           48 |
| chat / 600 / 1         |       19.055 |           18.835 |            7 |
| chat / 600 / 8         |       34.624 |           29.217 |           48 |

全部unique direct/gateway样本完整200；Responses100/600KB c1开销分别降低25.6%/27.1%，600KB c8降低36.7%，改善并非只在重复提示词矩阵出现。Responses1KB c1与Chat1KB c8较慢的格子也保留。每格8秒、nonce跨轮不同、多个优化共同变化；不视作单因素归因或稳定生产分位数。旧unique基线存在死信，短测数据保留为观察而非整轮验收通过。

每段到达窗口 20 秒，1000/2000 目标分别提供 20,000/40,000 arrivals，使用 1500/3000 workers；窗口结束后不补发，等待已发请求完成。TTFT 只包含完整成功请求；503 和未发 arrivals 单列，runner exit0 与账务通过不表示容量通过。

| API pool / GOGC / GOMEMLIMIT | 目标 RPS | 实发数 / 实际 RPS | 未发 arrivals | HTTP 200 / 503 | 成功 TTFT P95 ms | schedule lag P95 ms |
| ---------------------------- | -------: | ----------------: | ------------: | -------------: | ---------------: | ------------------: |
| 10 / 50 / 384MiB             |     1000 |   12,156 / 607.80 |         7,844 |     12,156 / 0 |         1028.873 |            7623.620 |
| 10 / 50 / 384MiB             |     2000 |   10,382 / 519.10 |        29,618 |    9,432 / 950 |         5486.225 |           14141.493 |
| 32 / 50 / 384MiB             |     1000 |   12,335 / 616.75 |         7,665 |     12,335 / 0 |         1046.836 |            7155.736 |
| 32 / 50 / 384MiB             |     2000 |    9,103 / 455.15 |        30,897 |      9,103 / 0 |         6605.880 |           14220.853 |
| 64 / 50 / 384MiB             |     1000 |   12,505 / 625.25 |         7,495 |     12,505 / 0 |         1217.207 |            6507.708 |
| 64 / 50 / 384MiB             |     2000 |    9,825 / 491.25 |        30,175 |      9,825 / 0 |         6263.156 |           13730.183 |
| 64 / 100 / 1GiB              |     1000 |   14,348 / 717.40 |         5,652 |     14,348 / 0 |          861.566 |            4891.575 |
| 64 / 100 / 1GiB              |     2000 |   17,570 / 878.50 |        22,430 |     17,570 / 0 |         2691.590 |           11168.314 |

| API 参数组             | API CPU 采样最大% 1000 / 2000 | API RSS 采样最大MiB 1000 / 2000 | 永久操作 / 流水 |
| ---------------------- | ----------------------------: | ------------------------------: | --------------: |
| pool10 / GC50 / 384MiB |                 552.7 / 676.6 |                   417.3 / 440.5 | 22,223 / 22,223 |
| pool32 / GC50 / 384MiB |                 551.7 / 656.5 |                   412.6 / 449.5 | 21,488 / 21,488 |
| pool64 / GC50 / 384MiB |                 550.7 / 638.6 |                   408.2 / 534.9 | 22,380 / 22,380 |
| pool64 / GC100 / 1GiB  |                 447.5 / 481.5 |                   669.7 / 971.2 | 31,968 / 31,968 |

四组十钱包的 Core / Redis / 永久流水余额均相等，最终十项 drain 均为0，financial/telemetry dead 均0；默认组的950个503仍保留为HTTP失败。增大pool消除了这几轮观察中的503，但单独增大pool没有改善高压吞吐。固定pool64后同时提高GC目标与内存目标，实际发出量增加、成功TTFT下降、CPU采样最大值下降，代价是RSS增加；这支持进一步评估该组合，不能分离两个参数的贡献。`GOMEMLIMIT` 是Go运行时软目标，不是RSS硬上限。

网关、worker、mock、发压器及数据存储共用八核；各进程采样最大值不同时，不能相加。显著schedule lag与未发arrival说明发压端和共享资源限制尚未排除，所有轮次都未达到1000/2000RPS目标，不能宣称独立网关容量上限。参数仅在私有fixture覆盖，生产默认配置未改。下一次容量定位应先将client/mock移到独立资源，并分别控制GC与内存变量，再做持续负载。

### 最终 CPU 采样观察

单独的 `server-extra-instrumented-pool64-gc100` 启用既有loopback pprof，使用相同最终业务二进制及pool64 / GC100 / 1GiB，另建隔离fixture。两段手动10秒CPU采样的开始/结束phase均处于各自1000/2000目标测量阶段，采样期间API CPU分别累计41.22s/41.20s（墙钟10.17s/10.18s）。另有一份自动触发profile，未标注精确负载phase，不用它作阶段归因。

手动两段 `internal/runtime/syscall.Syscall6` flat分别22.34%/22.01%，`encoding/json.(*decodeState).object` cumulative15.21%/12.67%，`encoding/json.checkValid` cumulative4.80%/4.56%。1000阶段streamScanner回调累计23.27%，包含其下游调用；累计比例相互重叠，不能相加或视作独立可删除开销。函数top不能判定具体syscall类别、哪个socket或JSON调用来源，也不能推算换transport/JSON库的收益；后续应先取得调用栈和flush行为分布，再评估批量stream写入/减少重复JSON处理，保留取消、事件顺序、首字与原生转发契约。

采样轮31,240笔操作/流水相等、钱包三方相等、十项drain0。开启profiler影响运行，不能把这一轮吞吐加入四组普通参数排名。原始profile只留在新机私有目录；本地仅导出函数摘要、时间/hash及无凭据结果。最终两组矩阵与四组普通参数实验导出102文件、采样导出17文件，均逐文件核对SHA256。

## 范围复查补齐与局部验证

- B10 日志身份 / 项目列表 / 成员列表批读：真实独立 Core / Telemetry PostgreSQL fixture 与 SQLite 都验证 1/20 行固定查询数。日志身份为 2 Core；项目列表为 4 Core；成员 hydration 为 4 Core + 1 Telemetry，完整管理页面含 count/list 为 6 Core + 1 Telemetry；空页只 count/list，权限拒绝不查库。历史软删 actor/Key/inviter、失联历史身份、暂停余额隐藏、首条禁用预算、内部 Key 排除、退款与 UTC 月初均保持已有语义。旧单成员金额 reader 的 SQLite 本地时区绑定问题同时修正为 UTC。日志 `.local-results/data-layer/console-batch-postgres-race.log`：真实双 PG + SQLite **4.839s**，无 SKIP；`console-batch-sqlite-race.log`：model **2.770s**、controller **4.527s**。
- B10 Key 预算批读：2/20 Key 的页级 facts 均 **10 SQL**、逐行 payload **0 SQL**；预算使用 Core 的分组 SUM，日/月 UTC、actor/Key/项目隔离、负退款、现代/旧流水去重及禁用首条 period cap 保持不变。PostgreSQL 与 SQLite 的 **201 预算 + 重复 ID** 验证只用 **3 条分组 SUM**，全部金额正确，现代副本没有双计。日志 `.local-results/gateway-bench/budget-batch/postgres-race.log`：model **3.137s**、controller **3.799s**，无 SKIP。
- B7 钱包/Key 实时可用额度：只读 Hot 快照原子检查 continuity，256 ID 分批，缺失/畸形/维护/重建/Redis 错误回落已查询的 Core 值，不初始化/修改钱。覆盖正负 int64 边界及 ±(2^53+1)、重复 ID、冷脚本 2 dispatch / 暖脚本每批 1 dispatch、付款人身份及暂停隐私；真实 Reserve/Settle 且不运行 writer 时，展示更新而 Core 金额保持原样。Key 的历史 used、总授予和 editor/mutation 基准不采用 Hot 投影。真实 PG + 独立 native Redis focused race：ledger **4.355s**、model **3.442s**、controller **6.917s**，无 SKIP，日志 `.local-results/gateway-bench/console-balances/focused-race.log`。项目列表仍 4 Core，2/20 Key 页仍 10 SQL、payload 0 SQL；组件测试实际使用 Hot，接口权限/契约测试验证接线，未声称完成整服务器 Hot UI 浏览器验证。
- A3.2 `auth.admission` 对普通与现代鉴权使用同一计时边界，在进入下游前结束。通道阻塞证明不把上游等待计入鉴权；下游 503 不改写已完成的成功计时，鉴权拒绝只统计一次，关闭指标不改变行为。日志 `.local-results/gateway-complete-checks/auth-metric-race-fixed.log`：middleware **4.843s**；先前独立 fixture 未初始化 SQL 列引用的失败日志保留，修正只在测试 fixture。
- A11 后台完整分词不再受 `GET_MEDIA_TOKEN` / `GET_MEDIA_TOKEN_NOT_STREAM` 控制，memo 输入随之去掉这两个无效条件；计费 fallback 的旧开关保持原语义。流式/非流式 × 四种开关组合、金额/供应商用量不变、每请求诊断和过期 carrier 不复活均通过。日志 `.local-results/gateway-complete-checks/shadow-race.log`：service **1.974s**、controller **1.986s**。
- Ops 四条耗时曲线有独立浏览器证据 `.local-results/gateway-chart/result.json`：pending 与 final 的位置、宽度、高度一致（1110×248）；1440px、390px、320px 的图例和页面无水平溢出，无 page error，空态已观察。这与通用 11 条 control/style smoke 分开记录。

整包回归揭露的问题保留失败日志并修复：历史钱包测试的非 UTC 时间写入改为 UTC 并覆盖 `[from,to)` 两端（`data-layer/wallet-utc-range-race.log`，**3.678s**）；可选 PG fixture 在 Skip 前改全局 Redis 标志的恢复顺序修正，legacy 状态测试使用独立 Core fixture 验证真正的到期/耗尽写入（`gateway-bench/budget-batch/model-isolation-after.log` **6.735s**、完整乱序 **8.054s**、相关重复乱序 race **3.710s**）。并发日志计数及轮换调度改为原子操作（`gateway-complete-checks/logger-concurrency-race.log` **3.179s**）；token alert 的测试 teardown 等待整项异步任务（包含实际邮件归档），门控 recorder 验证结算保持异步、真实归档完成后才可关闭数据库，20 次 focused race **15.727s**（`gateway-complete-checks/token-alert-lifecycle/focused-race.log`）。不将旧 `go-all.log` 或 `auth-shadow-router-race.log` 的整份失败改写为通过；最新整包验证另记录。

原始证据路径相对仓库根目录，保存在 `.local-results/`，不随 PR 发布；本文件只提交脱敏摘要。数据库包、私钥、环境文件、凭据和请求原文不进入 Git。

## 已验证的数据与迁移行为

| 项目                        | 已有结果与实际环境                                                                                                                                                                                                      | 证据 / 限制                                                                                                                                                                                                                                                                                                       |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 双库迁移与 schema           | 隔离 PostgreSQL 16 Core/Telemetry fixture 的迁移、按月分区、历史事实可见性及路由回退通过；另有显式 CLI 成功迁移到 schema **25**。较早的 `postgres-migrate.log` 明确是版本 24，不能把那个日志改写成 25。                 | `data-layer/postgres-test.log`（5.613s）、`data-layer/postgres-copy-final.log`（1.404s）、`data-layer/backup-schema25-migrate.log`（CLI version 25）；路径均在 `.local-results/`。不是生产历史全量转换验收。                                                                                                      |
| 离线 schema CLI             | 不启动 Redis 时，空库和已有历史的 digest 一次性迁移使用数据库专用进度写入；运行时管理选项发布仍要求 Redis。真实 PG 回归通过。                                                                                           | `.local-results/identity-offline-schema-regression.log`（1.312s）。测试迁移进度与运行时发布边界，没有通过初始化 Redis 绕过离线要求。                                                                                                                                                                              |
| Telemetry 复制、分区与历史  | 跨库复制可重跑；新日志 projection 优先于同请求旧路由事实；历史最终结果/被替代尝试/探测保持独立口径。分区迁移 audit、质量字段更新、重放后只保留一条完成日志、恢复旧可写路由实体表均有真实 PG 测试。                      | `TestPostgresTelemetryPartitionMigrationAndRollback`、`TestPostgresRollbackCopyOverwritesExistingHistoricalRow`；日志 `data-layer/postgres-test.log` / `postgres-copy-final.log`。首次旧档案与新表的 checksum 比较须在新写入、诊断更新、清理之前；档案不是实时镜像。                                              |
| Core dump / restore / proof | 实际 `pg_dump` 目录包恢复到另一空库，校正序列并 `ANALYZE`；逐表行数/内容哈希、钱包与 Key 金额证明匹配。schema 25 财务包使用非零余额及永久财务操作 fixture。                                                             | `.local-results/data-layer/financial-backup-schema25-restore.log` 包含 `Migration verification passed.`；`financial-backup-schema25-create.log` / `verify.log`。这是本地合成数据库，未复制生产数据。                                                                                                              |
| 归属守卫                    | 独立两连接 fixture 验证错库 ORM、Raw、UPDATE、JOIN、事务内 INSERT 被拒绝，事务中先前合法写入也回滚；已注册 SQL 引用覆盖限定语法与注释/字符串例外。                                                                      | `.local-results/data-layer/store-ownership.log`（0.438s）；`internal/store/ownership_test.go` 的行为 fixture 是 **SQLite 两连接**，不能写成真实 PG 全语法验证。运行时 PG 在 schema 验证后安装注册模型/已知表守卫；accessor 仍返回 `*gorm.DB`。没有编译期全面静态隔离，也没有完整 SQL parser / 动态 SQL 安全证明。 |
| A9.3 当前列契约             | PG fixture 保留当前 decision 17 列、outcome 42 列；JSON 契约排除已退休研究列，时间归一后与原对象一致。满字段合成 fixture：decision JSON 1060 bytes / `pg_column_size` 1064；outcome JSON 1471 / `pg_column_size` 1471。 | `.local-results/data-layer/routing-fact-size-contract.log`（0.941s）。不是生产 P99，不含索引、其他日志列、堆页面及旧档案；不能拿原 3.7KB 物理表均值与该 JSON 数字直接宣称生产压缩率。                                                                                                                             |

## 保留政策与迟到事件

真实 PG fixture 验证 `LOG_RETENTION_DAYS` 为 0、30、180、600 的独立行为及精确边界：0 关闭日志明细清理/日志分区删除；决策与尝试 90 天、最终结果 365 天、探测 30 天各按自身事件时间过期。旧准入分区里的近期媒体最终结果/近期探测仍保留；月末后续分区不漏月。

过期明细清除 IP、用户名/Key 名、会话/线程、用量事实、账单快照与诊断等字段，留下仍有效路由事实所需的最小 ID/时间 carrier。默认日志查询和用量导出隐藏 `LogTypeUnknown`；所有事实过期后删除 carrier。旧 `_unpartitioned_v24` 档案也参加各自保留清理；迁移/回退档案不自动永久保存。已保留恢复包及验收证明后，由副本演练明确删除重复档案，备份包仍按环境的备份保留政策管理。

永久财务操作、永久事件 receipt 和仍有效聚合不随明细删除。真实 PG redelivery 测试证明过期日志不会重建，session/digest/hour/day/model 统计不重复计数。迟到诊断在 SQLite 与 PG 的 single/batch 更新中 ACK，但不向已过期或无 active marker 的 Unknown carrier 恢复 `other`。新插入的 PostgreSQL 媒体 carrier 在 `other` 中带内部 active marker，允许临时计时；正式 consume 合并计时与新 facts、去掉 marker，保留期清理移除 marker / `other`，晚到 snapshot 不恢复 marker。

证据：`.local-results/data-layer/retention-policies-postgres.log`（2.870s）；`.local-results/data-layer/retention-rollback-regressions.log`（model/worker/service）；最新 `.local-results/data-layer/telemetry-counter-final-race.log` 包含 `TestPostgresRequestDiagnosticBatchMergesAndPrunes` 的过期 single/batch 回归。测试不能替代生产档案尺寸、政策配置与实际历史分布检查。

## 遥测批量、重放和数值正确性

实际 writer 以最多 500 个不同请求分组，另受 4000 条物理消息和 16MiB payload 上限约束，正常收集窗口不超过 1 秒。最小财务 receipt、完整 relay facts 及同批诊断在一个 Telemetry 事务中提交；新插入的永久 relay receipt 才可增加 session/day/hour/digest/model 统计，提交后再 ACK。500 个完整请求 / 重复 receipt / 多 patch、诊断先到后事实、过期 carrier、子线程精确计数、失败分组隔离及超大单事件 DLQ 均有真实 PG + Redis 回归。超大日志事实的失败不会删除 Core 永久财务事实；完整大日志可能进入 DLQ，不能描述为无限 payload 保证。

Go 的每项 token/cache/count/latency/histogram 聚合在 SQL 前检查负计数或有符号整数溢出；USD 转 microusd 拒绝 NaN、Inf、超范围。原始 UsageFact 在 Normalize 的 native-int 加法前检查；显式 digest 历史重建也在替换旧聚合前拒绝溢出。金额允许合法负值，包括退款和负毛利，不做 silent clamp。整批出错时 receipt/log/派生事实事务一起回滚，事件留给重试 / DLQ。已有 bigint 与 histogram 达到上限的 SQL 冲突也证明回滚，修正异常 seed 后同一请求可再次提交。精确上限值、signed money、两次重放均通过。

证据：

- `.local-results/data-layer/telemetry-writer-postgres-race.log`：PASS 11.576s，真实 PG + Redis grouped writer/consumer。
- `.local-results/data-layer/telemetry-writer-sqlite.log`：PASS 5.632s，portable writer / 分组与 bytes 边界。
- `.local-results/data-layer/telemetry-counter-final-race.log`：PASS **13.841s**，无 SKIP；23 个 malformed pre-SQL 子场景、stored counter/histogram rollback+retry、边界/退款/重放、500 请求 writer、先到诊断、DLQ、原子 RAM export 回归。
- `.local-results/data-layer/telemetry-counter-sqlite.log`：PASS 1.269s，raw UsageFact/native int、histogram/USD、历史 digest rebuild 及 legacy 入口。

这些检查保护 writer / rebuild 的持久化正确性；本次没有将全部只读报表 getter 改成 checked arithmetic，也没有宣称任意损坏的报表数据都能自动修复。可选 RAM usage export 属于提交后的 best-effort 投影，区别于上述 canonical durable rollups。

## 财务批量、退款、spool 与回退

独立真实 PG 财务批量测试证明 500 个全新永久操作仅一次 operation `INSERT ... RETURNING`，新操作不额外 SELECT，payer 与 Key 各一次有序 `UPDATE FROM VALUES`；混合 payer/Key、限额及无限额 Key、退款正确。same-operation ID 同批或已有不同 actor/预算/金额等不可变输入冲突均拒绝；canonical legacy decode/UTC/空数组等价重放保持不变。缺 payer/Key、已存 JSON 篡改及用户/Key SQL overflow 回滚整个 Core 事务。

永久 operation 的 Input 保留原预算证明；现代正常批次不新增逐请求 legacy `SpendBudgetCharge`。回退根据 operation + budget ID 唯一实体化，当前匹配定义决定派生周期，原不可变 Input 不改写。global budget 周期改变后 legacy SUM 不双计；已有 ProjectSpend 相同金额但错误 actor/Key 等归属冲突必须拒绝并保留原 daily 汇总。累计金额溢出也不删除源汇总。退款使用原财务输入/稳定 operation ID 同步 Hot 状态，正常消费及退款日志身份不碰撞。

fsynced spool 的财务重试忽略后续变化的 TelemetryEnvelope，保留第一次完整 envelope；金额、subject、operation/input 改变仍拒绝。实际测试覆盖 changed diagnostics/facts 与负计数异常；不等同于机器断电 / 磁盘损坏后的真实介质恢复。

证据：

- `.local-results/identity-financial-batch-regression-race.log`：PASS **23.283s**，无 SKIP；500 operation 常量 SQL 语句、混合/重放/冲突/overflow，以及既有 exact-money、cold snapshot、66k recovery、credit、continuity、decision-refund 回归。由 identity 执行，报告整理时只读复核日志。
- `.local-results/data-layer/financial-fixtures-final-race.log`：PASS **20.974s**，backup audit、原输入退款、预算定义变更及超过 PG bind limit 的 cold snapshot、global-period legacy rollback、错误归属与累加溢出拒绝。
- `.local-results/data-layer/spool-retry-regression.log`：PASS 2.372s；`.local-results/data-layer/rollback-period-regression.log`：PASS 0.643s。

旧 `.local-results/gateway-plan-real-stores-race-final.log` 有 fixture 遗留 Redis keys/旧 legacy-row 断言引起的失败，不能称为整份日志 PASS；具体用例由上述后续隔离回归替代。实际进程物理 kill/restart、5 分钟 PostgreSQL 全停后的生产形状 backlog 恢复仍未作为已验收项目。

## schema 25 财务备份 / RDB / age / R2

本地实际流程：停止写入的隔离 fixture → backup-audit → Core directory dump + Redis RDB + 全 spool 库存记录 + manifest → verify → 恢复至另一空 Core / 新端口 Redis → 逐表内容和金额 proof、同一 continuity、钱包/Key 数字验证 → age 加密与解密 → 解密目录再 verify。RDB 是整个 Redis 实例的所有 logical DB 快照，manifest 另记录网关 logical DB；恢复不能只凭 DB 编号忽略同实例其他数据。RDB 加载成功不代表生产 AOF/内存限制/绑定配置已经演练。

schema 25 fixture 存在非零钱包/Key 余额及永久财务操作；Core restore proof 匹配，Redis restore backup-audit 记录 wallets_verified=1、keys_verified=1，pending credit/counter-refresh/reservations 为零，continuity 一致。包内 manifest 为文件 bytes + SHA-256；age 解密后再次通过校验。私钥及明文包留在受限本地目录，不写入报告或 Git。

已有备份拒绝测试：pending credit、非空 spool、无财务 consumer group、未读事件、pending event；已提交 stream 的历史保留不被误判为待落库。离线 Python **unittest** 5 项通过：凭据不入 argv、文件修改/缺失拒绝、符号链接拒绝、伪装 `.age` 明文上传拒绝、没有 writers-stopped 证明前不执行外部命令。

R2 仅做 **dry-run**：使用合成 preview bucket / `example.invalid`，输出对象路径及加密包 hash；没有实际网络上传、下载验证、远端定时任务或历史对象清理。不能将此写成“每日 R2 备份已上线”。

证据位于 `.local-results/data-layer/`：`backup-schema25-migrate.log`、`financial-backup-schema25-create.log`、`financial-backup-schema25-verify.log`、`financial-backup-schema25-restore.log`、`financial-backup-schema25-redis-restore.log`、`financial-backup-schema25-decrypted-verify.log`、`financial-backup-schema25-r2-dry-run.log`、`backup-audit-unread-rejections.log`、`backup-offline-tests.log`。schema 25 包目录为 `financial-backup-schema25`，恢复 proof 应对包内快照比较；源一旦恢复接单，不能期待实时源哈希仍与旧快照相等。

## 修正后性能复验的环境中断

`.local-results/gateway-bench/final-observer-0ms-partial-environment-stall/` 保存修正后 0ms 的未完成运行：12 个短组合已产生，没有 `acceptance.json`、压力/暂停/最终对账证据，退出 143。不能把它作为完整性能 PASS 或正常 0ms 环境证明。worker 日志记录空表查询约 20–52 秒及 Redis I/O timeout；只读容器检查当时为 running/未paused、数据库连接 idle，没有观察到阻塞 SQL。macOS 电源日志在本轮期间记录 **51 条** Sleep/Wake/DarkWake 事件，包含 Clamshell Sleep；仅保留脱敏的时间、类型、睡眠原因于 `power-events.json`。这些说明测量被合盖/唤醒周期打断，不足以唯一归因每个尖峰。

确认进程组仍存活后才终止本任务 runner 及子进程，随后按确切名称清理自有 PG/Redis 容器；没有终止其他应用或删除历史证据。后续在本机开盖、持续唤醒时完成了 `final-awake` 同源 0/35ms 两轮，均无 Sleep/timeout，实际结果见最终矩阵。临时防闲置睡眠设置已随测试结束释放；已通过的功能与财务回归不代替数字门槛。

## 后台观察循环专项验证

财务 writer 的逐钱包健康/对账改为独立单循环；10 秒 tick、跨节点一分钟 reconciliation tick、revision/generation 与 repeatable-read 校验保留。取消后 join，报告函数仍串行调用；counter-refresh、spool、dead letter、collect、credits、reap 维护顺序保持。空 `(hot_seq, Core_seq]` 区间跳过 SUM，与旧查询结果等价。

- `.local-results/data-layer/ledger-observer-race.log`：ledger **12.742s**，native Redis + 测试 Storage，验证阻塞观察期间实际 Reserve/Settle 仍交付、单实例、错误报告、取消和 join。
- `.local-results/data-layer/ledger-observer-postgres-race.log`：model **15.889s**，独立 PostgreSQL 16 Core 与 native Redis，无 SKIP；阻塞观察时永久财务操作与 WalletLedger 各一条、quota 930 / used 70 已提交。相等/领先序号 **0 SUM SQL**，实际未同步正/负 credits **1 SUM SQL**；畸形序号仍拒绝，金额差 1 仍告警。
- 早期测试退出时把旧维护链的 `ErrUnavailable: context canceled` 当成业务错误，失败日志保留为 shutdown assertion before；只修正测试识别既有关闭错误，未改变财务运行时错误语义。

## 历史通用与专项 unit / race / Python 证据

| 实际结果                                                   | 日志                                                                 | 说明                                                                                                                               |
| ---------------------------------------------------------- | -------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| scanner 优化后的全 Go 测试通过                             | `.local-results/gateway-optimization/go-all-scanner.log`             | 执行 `go test -p 2 ./... -count=1`，退出 0；后续只改 bench 的工具已另做 focused / race，不把历史业务源码日志误称当前全部工具证明。 |
| scanner 优化后的 go vet 通过                               | `.local-results/gateway-optimization/go-vet-scanner.log`             | 完整命令退出 0，正常无诊断输出。                                                                                                   |
| 最新完整 service -race 通过，28.516s                       | `.local-results/gateway-complete-checks/service-race-final.log`      | logger / alert 生命周期修复后完整 service 回归，退出 0。                                                                           |
| 此前 middleware / router -race 分别通过，17.032s / 52.689s | `.local-results/gateway-complete-checks/auth-shadow-router-race.log` | 该文件的 service 当时失败，不能把整份日志写成 PASS；上述最新 service 回归替代该失败部分。                                          |
| 全 router -race 通过，54.793s                              | `.local-results/gateway-plan-router-race-final-marker.log`           | root 的全套回归替代此前 fixture 生命周期失败；显式 opt-in live upstream 不属于本地通过范围。                                       |
| router 原失败场景及新双库生命周期 -race 通过，29.643s      | `.local-results/data-layer/router-diagnostics-race-regression.log`   | shadow/timing 排空后才换连接/关闭 DB；两类字段保留、计费不改。专用 SQLite fixture 单写连接；没有用 DB 全局 atomic 掩盖错库。       |
| 数据 overflow / writer -race 通过，13.841s                 | `.local-results/data-layer/telemetry-counter-final-race.log`         | 真实双 PG fixture + Redis，未跳过 integration。                                                                                    |
| 财务批量 / 回归 -race 通过，23.283s                        | `.local-results/identity-financial-batch-regression-race.log`        | 协作方真实 local PG/Redis 结果。                                                                                                   |
| Python unittest 通过，5 tests                              | `.local-results/data-layer/backup-offline-tests.log`                 | 实际使用 unittest；没有独立 pytest 通过日志，不能改称 pytest PASS。                                                                |
| 后续 Python unit 通过，5 tests                             | `.local-results/gateway-bench/post-diagnostic-python-final.log`      | perf 侧工具 unit 日志同样为 unittest 输出；最终性能结果另补。                                                                      |
| 文档路径/链接检查通过                                      | `.local-results/gateway-complete-checks/docs-links-observer.log`     | 90 文件、21 topics、4 locales；最终报告更新后的重复检查另记 `docs-links-awake.log`。                                               |
| 秘密检查通过                                               | `.local-results/gateway-complete-checks/secrets-observer.log`        | 14 项 scanner unit 与 staged scanner 通过；最终报告更新后的重复检查另记 `secrets-awake.log`，不提交本地证据包。                    |

失败记录必须保留其状态：`router-diagnostics-race-before.log` 独立重现 setter/异步诊断竞态；`router-diagnostics-race-final.log` 全旧用例无 race、新增 SQLite 并发 patch 断言失败。后续专用 fixture 修正及 root 全套 race 才构成替代通过证据，不能把这些旧 FAIL 日志改写为 PASS。编译检查 `[no tests to run]` 也不是 correctness 测试。

## Claude 生产副本仍须独立完成的五项

1. **全 writer / spool / 在途库存与首次启用。** 在隔离生产副本盘点全部 API、jobs、回调资金来源及每个写节点 spool；先由旧版本处理未完成旧请求/媒体预占，逐笔分类路径的 `precharge-report` blocker 必须清零；首次 SQL 期初路径则按 schema 27 runbook 固定历史和余额、保留经审批豁免凭证，`ledger-bootstrap -writers-stopped -spool-dirs ... -ledger-proof ...` 精确覆盖历史 precharge 分类与证据。存在 ledger continuity 后 Hot 丢失必须 recover，不重新 bootstrap。不能虚构 provider 回调的持久队列；沿已有重试协议协调停止来源与未完成入账。
2. **同 schema 全量复制、校验与保留。** 完整旧副本先显式升级 schema 25，再 `partition-audit`、Core dump/restore、`verify -scope core`、`copy-telemetry`、`verify -scope telemetry`、`sequences`。audit/checksum 要在 writer/诊断/保留清理之前；显式空 `TELEMETRY_DATABASE_URL` 与 `LOG_SQL_DSN` 防止 dotenv 污染单库迁移。核对真实历史最终/被替代结果、金额与归属、独立保留边界和 archives 清理；记录真实列尺寸及包含索引/档案的空间收益。
3. **一致包与故障恢复演练。** 维护/全停写后使用真实全部 spool 和分类 proof，演练 `ledger-report → ledger-recover → ledger-drain-telemetry → ledger-resume → 启动 writers`，核对永久操作、钱包/Key/预算、pending、continuity 与 duplicate 防护。recover 中断只在同 proof / recovering 状态重跑；active 后 recover 是新一轮，resume 成功后重复调用会拒绝缺少维护。物理 kill/restart、5 分钟 PG 停机/backlog 和介质恢复应另取得真实环境证据，本报告不把进程内故障注入当作已完成这些验收。用户已选择两层身份快照严格 60 秒有效期：首次使用、到期、版本变化或必须权威 SQL 确认而 Core 不可用时返回 503；有效热状态只能按剩余期限有条件准入，已接受请求仍须持久保存。5 分钟停机不保证所有新请求持续可用，恢复后核对已保存结算并实测追平时间。
4. **远端备份与运维落地。** 独立保管 age 私钥，执行实际加密文件 R2 上传并记录对象身份/hash，下载到新目录解密/verify，再在新 Core/Redis 做 restore + proof / audit。接入既有运维调度、失败告警、备份保留与定期恢复演练；确认生产 AOF、Redis 最大内存/绑定和整个实例恢复范围。不把现有仅 PG 的 scheduler 当作新 PG+Redis+spool 一致备份。
5. **旧版回退与可重新升级的证明。** 保持维护、全部 writer 停机、财务/遥测/spool 排空，先保存一致包；必须 `prepare-legacy-rollback`，验证逐请求项目/预算投影与金额/subject、可写旧路由实体、遥测复制回 Core/序列，最终兼容 marker **23**。先在副本启动确切旧版本验证写路由与计费，不直接替换镜像/连接串；任一跨库步骤失败保持维护并重跑/恢复一致包。再升级时重新核对旧程序新增 SQL 财务输入及 Hot continuity，不沿用回退前的钱包热状态。

## 历史 Mac 同源性能矩阵

历史 Mac 有效环境、同一冻结源码 `final-awake` 完整对照：0ms **10/11**，35ms **10/11**；该环境数字门槛未全部通过。未调整门槛，未选取重试通过轮。

当时 807 个 runtime/tool 文件与 6 个二进制两轮哈希一致，冻结时源码 drift=[]；两轮 exit=1/1，environment_valid=true。归档 `final-awake-{0ms,35ms}/`，对照 `final-awake-pair-hashes.json`；每轮 `artifact-provenance.json` 记录 canonical JSON 与实际外部 run log 的哈希，继承的 scratch `run.log`/`run-final.log` 不作本轮证据。测试文件不在 runtime hash 清单中；该历史哈希不覆盖随后 scanner / Linux / 发压工具优化。

隔离 PostgreSQL 16 双库 / Redis 7、schema 25、显式 bootstrap；延迟组模拟数据库额外 RTT。身份 TTL 严格 60 秒；主机 overload 拒绝在合成压力中关闭，财务、权限、rate 检查保留。每个短组合的实际样本数见表，不构成生产稳定分位数或独特 prompt 性能证明。

TTFT ΔP50 是 gateway P50 减 direct P50；tail 从完整终态 SSE 事件到 EOF。每个短组合运行 8 秒；1/100/600KB 的相应 TTFT 目标为 5/15/50ms，c1 tail MAX ≤15ms、c50 tail P95 ≤50ms，全部门槛保持 [baseline](baseline.md) 定义。

| 协议/KB/c        | 样本0/35ms | TTFT ΔP50(ms) 0/35ms | tail P95/MAX(ms) 0ms | tail P95/MAX(ms) 35ms | API SQL 0/35ms | jobs SQL总数/req 0ms；35ms   | required Redis 0/35ms | 新连接比例0/35ms |
| ---------------- | ---------- | -------------------- | -------------------- | --------------------- | -------------- | ---------------------------- | --------------------- | ---------------- |
| responses/1/c1   | 6/6        | 5.918/5.459          | 6.788/6.790          | 6.816/7.526           | 0/0            | 129/21.500000；114/19.000000 | 3.000/3.000           | 0.0/0.0          |
| responses/100/c1 | 6/6        | 10.603/11.150        | 8.959/12.666         | 7.282/8.965           | 0/0            | 144/24.000000；131/21.833333 | 3.000/3.000           | 0.0/0.0          |
| responses/600/c1 | 6/6        | 32.955/27.272        | 9.767/11.744         | 6.728/9.294           | 0/0            | 174/29.000000；168/28.000000 | 3.000/3.000           | 0.0/0.0          |
| responses/600/c8 | 48/48      | 23.761/28.756        | 13.865/16.700        | 15.622/16.021         | 0/0            | 130/2.708333；119/2.479167   | 3.000/3.000           | 0.0/0.0          |
| responses/1/c32  | 192/192    | 18.865/20.184        | 24.407/28.679        | 23.996/28.654         | 0/0            | 144/0.750000；126/0.656250   | 3.000/3.000           | 0.0/0.0          |
| responses/1/c50  | 300/300    | 16.780/17.318        | 26.495/28.245        | 32.933/36.439         | 0/0            | 129/0.430000；120/0.400000   | 3.000/3.000           | 0.0/0.0          |
| chat/1/c1        | 6/6        | 4.845/3.838          | 7.983/13.080         | 5.679/7.863           | 0/0            | 127/21.166667；117/19.500000 | 3.000/3.000           | 0.0/0.0          |
| chat/100/c1      | 6/6        | 11.642/5.696         | 7.268/7.930          | 9.873/11.191          | 0/0            | 129/21.500000；116/19.333333 | 3.000/3.000           | 0.0/0.0          |
| chat/600/c1      | 6/6        | 20.246/18.887        | 8.575/9.210          | 6.984/10.202          | 0/0            | 171/28.500000；165/27.500000 | 3.000/3.000           | 0.0/0.0          |
| chat/600/c8      | 48/48      | 17.983/17.266        | 13.658/14.085        | 13.657/14.296         | 0/0            | 129/2.687500；118/2.458333   | 3.000/3.000           | 0.0/0.0          |
| chat/1/c32       | 192/192    | 15.447/13.786        | 20.205/22.015        | 18.095/20.261         | 0/0            | 130/0.677083；114/0.593750   | 3.000/3.000           | 0.0/0.0          |
| chat/1/c50       | 300/300    | 22.226/17.312        | 27.200/38.348        | 26.653/33.154         | 0/0            | 130/0.433333；157/0.523333   | 3.000/3.000           | 0.0/0.0          |

| 数字验收 flag                                                | 0ms  | 35ms |
| ------------------------------------------------------------ | ---- | ---- |
| `short_warm_api_zero_sql`                                    | PASS | PASS |
| `short_warm_redis_at_most_three`                             | PASS | PASS |
| `ttft_p50_body_targets`                                      | FAIL | FAIL |
| `single_request_tail_at_most_fifteen_ms`                     | PASS | PASS |
| `fifty_concurrent_tail_p95_at_most_fifty_ms`                 | PASS | PASS |
| `fresh_connection_ratio_below_one_percent`                   | PASS | PASS |
| `both_stress_cases_offer_five_hundred_rps_for_sixty_seconds` | PASS | PASS |
| `writers_paused`                                             | PASS | PASS |
| `settlement_visibility`                                      | PASS | PASS |
| `accounting`                                                 | PASS | PASS |
| `timing_fact_coverage`                                       | PASS | PASS |

### 实际未通过项

- 0ms responses 1KB/c1 TTFT ΔP50 5.918ms >5ms
- 0ms `ttft_p50_body_targets` FAIL
- 35ms responses 1KB/c1 TTFT ΔP50 5.459ms >5ms
- 35ms `ttft_p50_body_targets` FAIL

### 两类 500 RPS / 60 秒压力

| PG 额外 RTT / 场景             | 请求 / HTTP 状态       |     RPS | offered / 秒 | API SQL | jobs SQL总数 / 每请求 | 四窗 TTFT P95 ms                      | TTFT MAX ms |
| ------------------------------ | ---------------------- | ------: | ------------ | ------: | --------------------: | ------------------------------------- | ----------: |
| 0ms / single key               | 29996 / {"200": 29996} | 499.933 | 500 / 60     |      17 |       1413 / 0.047106 | 302.632 / 302.281 / 302.399 / 302.344 |     324.031 |
| 0ms / same project / ten keys  | 29992 / {"200": 29992} | 499.867 | 500 / 60     |      26 |       1333 / 0.044445 | 302.875 / 302.274 / 302.397 / 302.332 |     327.383 |
| 35ms / single key              | 29942 / {"200": 29942} | 499.033 | 500 / 60     |     309 |       1360 / 0.045421 | 302.423 / 302.243 / 302.225 / 302.999 |    2329.355 |
| 35ms / same project / ten keys | 29984 / {"200": 29984} | 499.733 | 500 / 60     |     288 |       1354 / 0.045157 | 302.495 / 302.534 / 302.437 / 302.642 |    1910.735 |

四窗 P95 均约 302–303ms，未观察到持续恶化；35ms 单 Key 最后窗较首窗增加 0.576ms。MAX 独立保留：35ms 两场景存在 2.329 / 1.911 秒的首字长尾，不能凭 P95 或 flag 声称无长尾。持续压力跨身份 TTL，权威重载 SQL 不应写成暖请求零 SQL。消费者成本依赖批次占用率，不宣称所有流量下 consumer ≤2 SQL。

### Core 结算提交、writer 暂停与财务对账

| Core 结算 / writer / 财务证据 |                0ms |               35ms |
| ----------------------------- | -----------------: | -----------------: |
| 样本                          |              61104 |              61042 |
| 缺失提交                      |                  0 |                  0 |
| EOF 后 commit P99 ms          |               1074 |               1480 |
| EOF 后 commit MAX ms          |               1132 |               1923 |
| P99 门槛 ms                   |               2000 |               3000 |
| Core 可见性                   |               PASS |               PASS |
| 永久操作 / WalletLedger       |      61710 / 61710 |      61587 / 61587 |
| 财务 / 排空 / 钱包一致        | PASS / PASS / PASS | PASS / PASS / PASS |
| 暂停 writer                   |               PASS |               PASS |
| 暂停请求 / HTTP 状态          |   48 / {"200": 48} |   48 / {"200": 48} |
| 暂停 API / jobs SQL           |              0 / 0 |              0 / 0 |

可见性只衡量 Core `redis_ledger_operations`（`kind=settle`）的 `pg_xact_commit_timestamp(xmin)` 相对客户 EOF，使用 PostgreSQL-minus-host 下界计算保守上界。它不证明 Telemetry 日志、看板或整个 Console 首次可见时间。P99 不等于所有请求保证；暂停 writer 的样本不计入常态 P99。8 秒 SIGSTOP/恢复不替代 5 分钟 PostgreSQL 物理停机、机器掉电或生产恢复验收。

| PG RTT | 时钟下界/上界 ms | drain 原始计数                                                                                                                                                                                                |
| ------ | ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0ms    | -8/70            | {"exposure": 0, "financial_dead": 0, "financial_lag": 0, "financial_pending": 0, "reserved_requests": 0, "spool_files": 0, "telemetry_dead": 0, "telemetry_lag": 0, "telemetry_pending": 0, "uncommitted": 0} |
| 35ms   | -8/70            | {"exposure": 0, "financial_dead": 0, "financial_lag": 0, "financial_pending": 0, "reserved_requests": 0, "spool_files": 0, "telemetry_dead": 0, "telemetry_lag": 0, "telemetry_pending": 0, "uncommitted": 0} |

| RTT/钱包ID |     SQL余额 |   Redis余额 | WalletLedger和 | 一致 |
| ---------- | ----------: | ----------: | -------------: | ---- |
| 0ms/2      | 99967771943 | 99967771943 |    99967771943 | PASS |
| 0ms/3      | 99997003635 | 99997003635 |    99997003635 | PASS |
| 0ms/4      | 99997004607 | 99997004607 |    99997004607 | PASS |
| 0ms/5      | 99997005579 | 99997005579 |    99997005579 | PASS |
| 0ms/6      | 99996697429 | 99996697429 |    99996697429 | PASS |
| 0ms/7      | 99996389279 | 99996389279 |    99996389279 | PASS |
| 0ms/8      | 99996750545 | 99996750545 |    99996750545 | PASS |
| 0ms/9      | 99996751517 | 99996751517 |    99996751517 | PASS |
| 0ms/10     | 99997059667 | 99997059667 |    99997059667 | PASS |
| 0ms/11     | 99997367817 | 99997367817 |    99997367817 | PASS |
| 35ms/2     | 99967804568 | 99967804568 |    99967804568 | PASS |
| 35ms/3     | 99997007038 | 99997007038 |    99997007038 | PASS |
| 35ms/4     | 99997007521 | 99997007521 |    99997007521 | PASS |
| 35ms/5     | 99997008980 | 99997008980 |    99997008980 | PASS |
| 35ms/6     | 99996699858 | 99996699858 |    99996699858 | PASS |
| 35ms/7     | 99996392199 | 99996392199 |    99996392199 | PASS |
| 35ms/8     | 99996753463 | 99996753463 |    99996753463 | PASS |
| 35ms/9     | 99996755892 | 99996755892 |    99996755892 | PASS |
| 35ms/10    | 99997061124 | 99997061124 |    99997061124 | PASS |
| 35ms/11    | 99997371218 | 99997371218 |    99997371218 | PASS |

### 二进制 provenance

| 二进制  | 两轮共同 SHA-256                                                   |
| ------- | ------------------------------------------------------------------ |
| gateway | `344d6f93e07ccafde7e664b70e78a379b2476fd715eb3c0b5b36f49a19148e78` |
| worker  | `0a1b8634ea7a62bba79eca86ac42400ef43551a715b47b1465e519104982ba23` |
| migrate | `68fd347cda2695e3e70919e5bc8aa542c8fe5991fee9d7679ff75daf3095aa79` |
| mock    | `dc47b25cf835b2da94da766eee08e10701b141029aa4f6ae9e2a46ec9c0cf323` |
| bench   | `9ce338016ff13fda0266f88407b13e3bd13bf1139defa147d5933e6109e69b04` |
| pgdelay | `c28aa972662b36cb2b105e025943d605825d6e0ef367ba0ec9c32a365d73a6f1` |

### 历史完整失败与环境中断

历史 `final-complete`（观察循环修正前）0ms 10/11、35ms 9/11，保留三项真实失败：Chat 1KB/c1 5.217ms >5ms；Responses 1KB/c1 5.051ms >5ms；35ms Core 结算提交 P99 3154ms >3000ms。`final-observer-0ms-partial-environment-stall` 因睡眠中断，没有完整 acceptance，不构成通过证据；旧结果均不能替代当前源码对照。

### 已有功能验证与历史失败

Responses 最终 usage / 元数据处理后停止等待逻辑终态后的上游 EOF，再执行 32KiB / 50ms 有界 drain。延迟 EOF gate、原生/桥接 completed/incomplete/failed、完整事件字段与真实连接复用有专门回归；计费和错误语义保持不变。最新全 Go、vet、完整 service race 与 B7/B10/A3/A11 局部回归见上面的证据表。

Web typecheck、brand、格式/lint、558 单元测试、构建/体积、四语言检查、11 项 control/style 浏览器检查及独立四曲线图表检查通过；gzip 首屏 278.67 / 280KiB，VChart 335.39 / 350KiB。Docs typecheck/lint/build 为 84 页面 + 404。秘密/品牌/错误出口/文档链接与 diff 检查记录在通用验证表和本地日志。

旧 `final-terminal` 冻结源码 0ms 为 10/11，Responses 1KB/c1 5.650ms >5；35ms 为 11/11，旧 source entries 803。旧数值、其他失败轮与手动中止的 `final-admission-0ms-partial-aborted/` 都保留原状态，不能拿旧 PASS 替代当前源码结果。后续确有准入分配优化与 A3/A11/B10/B7 范围修正，因此本节按最新冻结源码重新完整验收；未为了通过而放宽门槛。

### 性能结果的使用边界

合成压力重复输入，精确文本 shadow memo 仅完全相同输入命中；不能把命中收益推广到独特生产 prompt。文件输入及失败不缓存，不降低 token 精度或跳过大请求计费。若数字未达，提交评审是否接受明确差距仍由用户决定；生产副本迁移、恢复和回退仍须由 Claude 独立验收。
