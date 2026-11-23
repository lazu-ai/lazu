# Lazu Brand — Canonical Reference

> 仓库内品牌母版。所有锁定决议先在这里登记，再同步到产品代码。未锁定项不得被实现稿反向定义。

## Locked visual direction · G1（2026-10-06）

- **Locked:** 2026-10-06
- **Definition:** G1 = C2 减法 + 中性灰白底 + F2 青为结构色
- **Supersedes:** 02 云母的地面（三层暖纸渐变 + 双环境光 + 64px 网格）、悬浮圆角半透明侧栏、
  卡片 `backdrop-filter`、长春花蓝证据色、图表「墨」主序列，以及首页的 WebGL 地面着色器。
- **Keeps:** 印章 Logo 与 Instrument Serif 字标、控制台排版与间距、墨主按钮、选中态三样、
  静默滚动、paper states 与 feedback 的结构、官网首页版式。

核心规则：

1. **地面**：一块平涂中性灰 `#F5F5F6`，没有渐变、环境光与网格。
2. **表面**：白纸 `#FFFFFF` + 一根 hairline `rgb(20 22 28 / 10%)`；`surface-muted` 为 4% 墨，
   不再带黄。实色表面不加 `backdrop-filter`。
3. **侧栏**：贴左边，与页面同底 `#F5F5F6`，右侧一根 hairline；没有卡片、圆角、阴影与模糊。
4. **青 = 结构色** `#1F6F6B` / strong `#175855` / soft `rgb(31 111 107 / 11%)`（深色 `#4FB3AD` /
   `#7CCBC5` / 14%）：链接、当前导航项（整行淡染 + 青字 + 青图标）、焦点、选中控件、
   图表主序列（柱为青，折线为 strong 青）。仍不做大面积填充。预算、额度这类金额条用墨，
   和旁边的数字同色，不算图表。
5. **主按钮 = 墨**：`--lazu-action` 不变，任何范围里的 `.lazu-button--primary` 都是墨，
   不随 `--ui-accent` 变青。
6. **朱砂 `#C9452A` 不进 token**：只写在官网首页首屏的眉行和标题强调字上，全站只有这两处。
   眉行是 11px 小字，为过 AA 4.5:1 用加深的 `#B83E24`（深色 `#E0603F`）；标题是展示字号，保持 `#C9452A`。
7. **提醒条**：白底 + 1px 墨边；语义仍只由图标与标题字色承载（修订 `Locked feedback system` 的 hairline 边）。
   总览「需要处理」条同理，标签用墨。
8. **表头**：11.5px、字距 .04em、字重 500、`text-muted`。
9. **深色 = B 中性近黑**（2026-10-07）：底 `#0F0F11`（侧栏同底），卡片 `#18181B`，弹层 `#1D1D21`，
   线 8% 白（强调 14%），正文 `#ECECEE` / 次要 `#9B9DA4`，无阴影；主按钮反白（`#ECECEE` 底），
   青提亮为 `#4FB3AD`；提醒条用 14% 白线代替墨边。

落地：`tokens/colors.css` → `bun run brand:sync`；签名层 `apps/web/src/styles/yunmu.css`
（文件名沿用）；视觉守护 `apps/web/tests/smoke/yunmu-visual.spec.ts`。

## Locked page-UX revision（2026-10-14）

- **Rules:** [yunmu-console.md](foundations/yunmu-console.md) → "Locked page-UX revision"

在 G1 之上收齐五件共享件：一套页头几何；切换控件只留下划线 Tab（换视图）与分段框（筛表格）；
提醒条白底墨边；空态只用虚线位或一句纯文字；主题开关单色无回弹。模型页标题改用控制台统一标题。

## Console rules inherited from Yunmu（2026-08-20）

> 02 云母的暖纸地面、双环境光、悬浮半透明侧栏与长春花蓝证据色已于 2026-10-06 被 G1 取代（历史配方见 Git）；以下结构与交互规则继续有效。

- **Canonical foundation:** [yunmu-console.md](foundations/yunmu-console.md)
- **选中态**：只用底色 + 字重 + 前景色三样，禁止竖线、箭头、括号与扫光。
- **静默滚动（LOCKED 2026-08-22）**：任何滚动容器都不显示滚动条——不画滑块、不画轨道、不留槽位。滚动行为与键盘导航照常；唯一实现在 `tokens/ui.css` 末尾的 `*` 全局规则（随 `brand:sync` 进入 `brand.generated.css`）。**组件不得再局部声明 `scrollbar-width` / `::-webkit-scrollbar`。**
- **控制台信息架构**：用户侧主任务锁定为 5 页（总览 / API Keys / 用量与日志 / 钱包 / 个人设置，IA 修订 `S1 钱包独立`）；模型目录仍是工作台入口，文档不受模块开关影响，平台管理按站点角色呈现，不再有作用域切换器。
- **关键页面**：模型页为卡片广场 + 详情模态（`pages/Routes/`）；定价运营页 `pages/PricingOps/`；客户账户页 `pages/Organizations/` 使用现有用户/分组/钱包/定价契约，详情自 2026-11-19 起改为居中弹窗（不再用抽屉，见 `foundations/yunmu-console.md` 规则 7）；个人与平台设置共用 `label | value + action | explanation` 松间距行系统。

## Locked paper states · 系统状态面（2026-09-01，整页 2026-09-29 修订）

- **Decision:** 整页 404 / 403 / 崩溃 / 功能未开启 / 一次性结果页 = `状态标记 + 一句话`（`StatusPage`）；可操作空态 = `虚线位`，无操作空态 = `纯文字`；行内错误 = `断线`；加载 = `发丝线小圈`
- **Rules:** [paper-states.md](foundations/paper-states.md)

整页状态居中：一枚状态标记（HTTP 码，或一个英文斜体词，是整页上唯一的展示衬线）、
一个无衬线标题、一句话、可选的一枚上下文胶囊、最多两个按钮、只在确有相近页面时出现的
「要找的是不是」。加载 / 空态 / 行内错误每天出现几十次，一律零装饰。

## Locked feedback system · 纸上批注 + 墨条 Toast（2026-08-28）

- **Decision:** inline / banner / 表单 / 公告条采用 `A 纸上批注`；toast 采用 `C 墨条`（空态与整块状态面归 `Locked paper states`）
- **Rules:** [feedback-system.md](foundations/feedback-system.md)
- **Tokens:** `tokens/colors.css` 的 `--lazu-info*` 与 `--lazu-toast-*`；`tokens/ui.css` 的 `--lazu-radius-notice` / `--lazu-radius-toast` / `--lazu-notice-icon` / `--lazu-shadow-toast`

页面内的提示一律长在纸上：`surface-raised` 底 + 1px 墨边（深色 14% 白线），语义只由图标与标题字色
承载，禁止语义色填充容器、左粗边强调条与虚线语义。toast 复用主按钮的墨/纸反转，语义色只出现在图标上。

## Inherited foundations

`01 Kumo Native / Warm Utility`（2026-07-11）的暖橙色板已退役，保留其语义表面、发丝线、真实浮层阴影、1.5px 图标、120–180ms 无回弹动效与「事实 → 影响 → 一个动作」文案基线。

Current shared rules:

- [Colors](foundations/color-system.md): semantic palette (G1 values since 2026-10-06); values come from tokens.
- [UI composition](foundations/ui-system.md) and [components](foundations/component-contracts.md).
- [Public web](foundations/public-web-system.md) and [gateway product boundaries](foundations/gateway-product-system.md).
- [Quiet Precision](foundations/quiet-precision.md): de-carded KPIs, tabular figures, monochrome + single accent charts, inline receipt.
- Typography: Geist for Latin/numbers, system CJK, IBM Plex Mono for code/IDs; Instrument Serif (`--lazu-font-display`) for public display headings and the figures beside them, never under 20px. Console headings and display money are sans throughout (amended 2026-11-21; see [yunmu-console.md](foundations/yunmu-console.md#console-type-2026-11-21)).

## Locked logo system · Seal（2026-09-20）

- **Selected direction:** `C · 印章 / Seal` + 字标 `W1 · Instrument Serif「Lazu」`
- **Rules:** [logo-system.md](foundations/logo-system.md)
- **Canonical assets:** `assets/logo/`
- **Supersedes:** `E · 配重方柱 / Counterweight`（2026-08-01）

正式标志是一枚圆形印章：圆容器里沉在左下的几何 `L` 与右上一个圆点。字标是
Instrument Serif Regular 的首字母大写 `Lazu`。浅色默认 Graphite 深色容器 + Paper 图形；暗色使用 Paper 白底容器 + Graphite 图形。所有资产由 `assets/logo/generate-assets.mjs` 从同一几何母版生成，控制台侧栏、页脚与对外触点必须使用 `assets/logo/` 正式资产。

## Surface decisions index

| Surface | Status | Rules |
| --- | --- | --- |
| Console shell, top bar, review controls, audit fixes, overview + sessions, model status cards C′, wallet, usage & logs (M3 + L1 + S1) | Locked | [yunmu-console.md](foundations/yunmu-console.md) |
| API Keys & connect dialog (B + E, multi-address) | Locked | [api-keys.md](foundations/api-keys.md) |
| Model operations ledger (R1–R5, R6/R6b, E1, ratio pricing) | Locked | [model-ops-ledger.md](foundations/model-ops-ledger.md) |
| Team projects & reasoning-effort cap (A2 填线) | Locked | [team-projects.md](foundations/team-projects.md) |
| E-mails (C 分层、付款邮件墨头一封版、用量周报与月报) | Locked | [emails.md](foundations/emails.md) |
| Public web, Docs B workbench, SEO surfaces & 运营 (DUBRIO LLC footer/about) | Locked | [public-web-system.md](foundations/public-web-system.md) |

### Key locked constraints summary

- **Dashboard incident treatment (2026-07-29):** 正常状态不显示独立“服务状态”区块；错误率达降级阈值时在主内容显示紧凑 inline notice，持续记录进入全局 Status Center。
- **Team projects (2026-09-10 / 09-28):** 只有一种 Key（属于项目和一名成员）；移出/离开立即停用（10 分钟撤销窗口不变）；邀请链接 3 天有效；每人每项目最多 200 把未删除 Key；思考深度上限 `minimal < low < medium < high < xhigh < max` 三层只收窄不拒绝，控件为等宽英文词 + 填线，成员详情为 520px 居中弹窗。
- **Usage & logs (2026-09-29):** 线路标签挂模型名后（无分组列），失败只标状态格；行内展开 M3 收据 + F2 账本 + S1 官方价对比（计费时快照，省 < 1% 不显示，不提供「查看原始数据」）。
- **E-mails (2026-09-23 / 09-30):** 按新闻归入墨头 / 纸面 / 运维三层，640px 外框、卡片外页脚、一个主按钮 + 一个文字链接；付款成功只发一封英文邮件（不发 Invoice PDF，指向 Stripe hosted Invoice 页）；后台不再编辑邮件模板。
- **Public web & Docs (2026-09-23 / 09-29):** Docs 采用 `B · 工作台`（接口页分栏 + 深墨代码块 + `Copy page ▾` + 实时 Send request）；公开页头 模型 · 文档 · 博客 · 常见问题，页脚含 DUBRIO LLC 法律行；运营台四标签（上新 · 公告 · 博客 · 配图）。
