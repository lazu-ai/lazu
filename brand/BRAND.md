# Lazu Brand — Canonical Reference

> 仓库内品牌母版。所有锁定决议先在这里登记，再同步到产品代码。未锁定项不得被实现稿反向定义。

## Locked visual direction · G1（2026-10-06）

- **Locked:** 2026-10-06
- **Definition:** G1 = C2 减法 + 中性灰白底 + F2 青为结构色
- **Canvas:** `archive:brand/canvases.md`（「控制台 · G1 全页面」，29 张真实页面）
- **Decision history:** `archive:brand/explorations/positioning-team-stability-2026-10/`（实施单、注入式预览样式
  与未选方向；只作记录，不约束生产）
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
   青提亮为 `#4FB3AD`；提醒条用 14% 白线代替墨边。比较稿见画布
   `archive:brand/canvases.md`「深色模式」页（A 石墨 / C 一层底 / D 青墨未选）。

落地：`tokens/colors.css` → `bun run brand:sync`；签名层 `apps/web/src/styles/yunmu.css`
（文件名沿用）；视觉守护 `apps/web/tests/smoke/yunmu-visual.spec.ts`。

## Locked page-UX revision（2026-10-14）

- **Canvas:** `archive:brand/canvases.md`（「页面体验修改稿」，共享件 + 9 张页面）
- **Audit:** `archive:brand/explorations/page-audit-2026-10/index.html`
- **Rules:** `foundations/yunmu-console.md` → "Locked page-UX revision"

在 G1 之上收齐五件共享件：一套页头几何；切换控件只留下划线 Tab（换视图）与分段框（筛表格）；
提醒条白底墨边；空态只用虚线位或一句纯文字；主题开关单色无回弹。模型页标题改用控制台统一标题
（卡片仍按 Seal refinement 保持原样）。

## Superseded · 02 云母 Yunmu (Console, 2026-08-20 → 2026-10-06)

> 地面、表面、侧栏、证据色与图表主色已被 G1（2026-10-06）取代；下列条目中未被取代的部分继续有效。

- **Locked:** 2026-08-20
- **Approved exploration:** `archive:brand/explorations/console-f3-2026-08/ia-4skins.html`, skin `V2 云母`
- **Locked IA refinement:** `S1 钱包独立`；历史选择板已在决议固化后清理
- **Canonical foundation:** `foundations/yunmu-console.md`
- **Supersedes:** 01 Kumo Native 的"暖橙动作色"规则（第 2 条）。暖橙全面退出主按钮、图表与选中态。
- **配方来源:** cerul-platform `console-restored.css`（L1 冷纸），经 lazu 适配；共享的是材质语法，不是 cerul 品牌资产。

核心规则：

1. **地面**：三层暖纸竖向渐变 `#fbfaf7 → #f2f2ef → #eaebe9`，左上冷蓝环境光 `rgb(197 216 255 / 52%)` + 右上暖杏环境光 `rgb(247 219 184 / 24%)`，加 64px 网格纹理（opacity .34，中心径向 mask）。深色为零色偏石墨 `#1d1d1f → #171718 → #121213`，只保留冷蓝光。
2. **主按钮 = 墨/纸反转**：浅色墨底 `#202329` 纸字，深色纸底墨字。hover 只做位移 + 软阴影，不改色不发光。
3. **证据色 = 长春花蓝** `#88a5f2` / strong `#5e79cf`（深色 `#9fb6f7` / `#b7c9fa`），只用于链接、命中、峰值标注、focus 与少量 chip；不做大面积填充，不做常驻选中底。
4. **图表禁用品牌橙**：主序列用长春花蓝或墨色，辅序列用中性灰；语义状态色只表达状态。
5. **表面**：半透明暖白 `rgb(255 252 247 / 92%)` + hairline `rgb(60 66 78 / 12%)`；侧栏为悬浮圆角半透明面板。
6. **选中态**只用底色 + 字重 + 前景色三样，禁止竖线、箭头、括号与扫光（沿用既有铁律）。
7. 排版、图标、动效、状态与文案规则见下方 Inherited foundations 和现行 UI / component foundations。
8. **与 cerul 的区分**：lazu 保留自己的 wordmark/brand mark 与信息架构；若后续两产品视觉混淆，先调整强调色色相，不动材质语法。
9. **静默滚动（LOCKED 2026-08-22）**：任何滚动容器都不显示滚动条——不画滑块、不画轨道、不留槽位。一条灰色竖轨划过悬浮纸面会直接破坏整套系统赖以成立的「一张纸」错觉，而且是读者没有要求的 chrome。
   滚动行为本身不受影响：滚轮、触控板、触屏、键盘（方向键 / PageUp / PageDown / Home / End）与 `scrollIntoView` 全部照常，焦点轮廓照常移动；被去掉的只有那条被绘制出来的杆子。
   唯一实现在 `tokens/ui.css` 末尾的 `*` 全局规则（随 `brand:sync` 进入 `brand.generated.css`）。**组件不得再局部声明 `scrollbar-width` / `::-webkit-scrollbar`，更不得把滑块或轨道画回来。**

落地状态（2026-08-20）：

- 公开页（`/`、`/login`、`/register`）也已切到云母：`tokens/public-web.css` 里的
  `--lazu-web-landing-*` 独立颜色层已删除，落地页与身份页直接消费 `--lazu-*`；
  落地页地面由一段全屏 fragment shader 绘制（`pages/Home/components/YunmuGround.tsx`），
  画的就是本节第 1 条锁定的三层暖纸 + 双环境光 + 64px 网格，没有 WebGL 时退回静态渐变。

- `tokens/colors.css` 已切到云母调色板并 `brand:sync` 到 `apps/web/src/styles/brand.generated.css`。
- 签名层实现在 `apps/web/src/styles/yunmu.css`（最后加载）：三层暖纸地面 + 双环境光 + 64px 遮罩网格、悬浮圆角侧栏、墨/纸主按钮、中性选中态。
- 用户侧主任务锁定为 5 页（总览 / API Keys / 用量与日志 / 钱包 / 个人设置）；模型目录仍是工作台入口，文档不受模块开关影响，平台管理继续按角色呈现。历史 Billing、TopUp、Files 与 Access 入口已移除，能力分别由钱包、用量与日志和个人设置承担。完整约束见 `foundations/yunmu-console.md`。
- 模型页改为卡片广场 + 详情模态（`pages/Routes/`），含稳定 / Agent 专线两条线路与分组折后价。
- 定价运营页 `pages/PricingOps/`：双通道价目 + 计价方式（按 token / 按次 / 按时长）+ 跟随所选模型的发布历史。
- 客户账户页 `pages/Organizations/`：客户台账 + 单一账户运营抽屉，锁定稿为
  `archive:brand/explorations/platform-accounts-2026-09/a-v3.html`；只复用现有用户、Key、钱包、
  用量与等级合同，不新增组织实体或虚构权限能力。
- 设置界面锁定为 2026-08-27 refinement：个人设置与平台设置共用方案 1 的
  `label | value + action | explanation` 行系统，间距采用「松」；平台设置按方案 B
  收敛为账号与接入 / 计费与价格 / 模型与上游 / 可观测性四类。完整约束见
  `foundations/yunmu-console.md` 的 `Locked settings surfaces`。
- 权限按两轴落地：平台区由站点角色决定，控制台不再有作用域切换器（`helpers/auth.tsx`、`SiderBar.tsx`）。
- 视觉一致性由 `tests/smoke/yunmu-visual.spec.ts` 守护；`brand-matrix.spec.ts` 已更新到云母色值。
- 组织运营已按两层租户模型收敛到现有用户、分组、钱包与定价层：`pages/Organizations/` 使用 `/api/user/*` 与 `/api/user-tier/*`，不再规划一套重复的组织实体 schema 或 `/api/organization/*`。

## Locked paper states · 系统状态面（2026-09-01，整页 2026-09-29 修订）

- **Approved exploration:** 整页 `archive:brand/explorations/error-pages-2026-09-a/index.html`（方向 A）；卡片 `archive:brand/explorations/error-pages-2026-09/index.html`
- **Decision:** 整页 404 / 403 / 崩溃 / 功能未开启 / 一次性结果页 = `状态标记 + 一句话`（`StatusPage`），取代原 `折角 / 封条 / 撕边`；可操作空态 = `虚线位`，无操作空态 = `纯文字`；行内错误 = `断线`；加载 = `发丝线小圈`
- **Rules:** `foundations/paper-states.md`

整页状态居中：一枚状态标记（HTTP 码，或一个英文斜体词，是整页上唯一的展示衬线）、
一个无衬线标题、一句话、可选的一枚上下文胶囊、最多两个按钮、只在确有相近页面时出现的
「要找的是不是」。加载 / 空态 / 行内错误每天出现几十次，一律零装饰——一枚小圈、一个
虚线位、一根断线。该决议废除了状态卡的图标框流派（含 `feedback-system.md` 的错误空态
图标框），并取代 `RecoveryState` 的事实表版式。

## Locked feedback system · 纸上批注 + 墨条 Toast（2026-08-28）

- **Approved exploration:** `archive:brand/explorations/feedback-2026-08/feedback-4ways.html`
- **Decision:** inline / banner / 表单 / 公告条采用 `A 纸上批注`；toast 采用 `C 墨条`（空态与整块状态面已于 2026-09-01 移交 `Locked paper states`，见上节）
- **Rules:** `foundations/feedback-system.md`
- **Tokens:** `tokens/colors.css` 的 `--lazu-info*` 与 `--lazu-toast-*`；`tokens/ui.css` 的 `--lazu-radius-notice` / `--lazu-radius-toast` / `--lazu-notice-icon` / `--lazu-shadow-toast`

页面内的提示一律长在纸上：`surface-raised` 底 + hairline 边，语义只由图标与标题字色
承载，禁止语义色填充容器、左粗边强调条与虚线语义。toast 是唯一例外，复用主按钮的
墨/纸反转，语义色只出现在图标上。`--lazu-info` 从图表色 `--lazu-data-blue` 独立出来，
图表配色与状态色不再互相牵动。该决议同时终结了 `lazu-banner` 与 `lazu-inline-notice`
的双组件并存、通知类五种圆角与四种图标尺寸。

## Inherited foundations and historical direction

`01 Kumo Native / Warm Utility` was selected on 2026-07-11 and its palette
was superseded by Yunmu on 2026-08-20. The old warm-orange action and chart
rules are historical, not current constraints. What it established and Yunmu
kept: semantic surfaces and hairlines, with shadows only for real elevation;
readable page hierarchy, task headers and efficient resource lists; icons at
about 1.5px, readable at 16px; direct 120–180ms motion without bounce,
respecting reduced motion; state carried by text and shape as well as color;
copy that states the fact, the impact and one action. Its colour tables and
superseded recipes remain in Git history.

Current shared rules:

- [Colors](foundations/color-system.md): semantic palette (G1 values since 2026-10-06); values come from tokens.
- [UI composition](foundations/ui-system.md) and [components](foundations/component-contracts.md).
- [Public web](foundations/public-web-system.md) and [gateway product boundaries](foundations/gateway-product-system.md).
- Typography: Geist for Latin/numbers, system CJK, IBM Plex Mono for code/IDs; Instrument Serif (`--lazu-font-display`) for public display headings and the figures beside them, never under 20px.
- Icons: approximately 1.5px, readable at 16px; state uses shape and text as well as color.
- Motion: direct 120–180ms transitions with no bounce; copy states fact, impact and one action.

## Locked logo system · Seal（2026-09-20）

- **Selected direction:** `C · 印章 / Seal` + 字标 `W1 · Instrument Serif「Lazu」`
- **Decision history:** `archive:brand/explorations/brand-refresh-2026-09/`（四套整体方案 → 字体海选 24 款 → 四个字标方向）
- **Rules:** `foundations/logo-system.md`
- **Canonical assets:** `assets/logo/`
- **Supersedes:** `E · 配重方柱 / Counterweight`（2026-08-01）

正式标志是一枚圆形印章：圆容器里沉在左下的几何 `L` 与右上一个圆点。字标是
Instrument Serif Regular 的首字母大写 `Lazu`，与几何标志刻意反差——标志是印，
字标是签名。线上默认使用 Graphite 深色容器 + Paper 图形；暗色主题使用 Paper 白底
容器 + Graphite 图形。Primary、Stacked、Wordmark、Icon、favicon、PWA 和社交分享
资产均由 `assets/logo/generate-assets.mjs` 从同一几何母版与冻结的字标轮廓生成。
控制台侧栏、页脚与 Studio 一律使用 Primary 锁定件，页面上不再出现 Geist 排的
「lazu」当作标志。

同一决议把字标字体升为产品的 **display 字体** `--lazu-font-display`：公开页的
display 标题、章节标题与标题旁的大数字用衬线，20px 以下一律不用；界面正文、控件
与表格继续 Geist，代码与 ID 继续 IBM Plex Mono。公开首页按 `C · 印章` 方向重排为
编辑式单栏（`foundations/public-web-system.md` 第 01 / 03 / 16 / 17 / 19 条）。

## Locked deepening · Quiet Precision（2026-07-19）

- **Approved direction:** `A · Kumo 精密 / Quiet Precision`（首页、Dashboard、模型广场、用量与日志四页全选 A）；历史选择板已在决议固化后清理
- **Rules:** `foundations/quiet-precision.md`
- **Tokens:** `tokens/ui.css` 新增 data-display 段；色值、字体、图标、动效 token 不变

该决议是 Kumo Native 的深化而非替代：KPI 去卡片化（发丝线分栏）、数据字阶（34/26px 表格数字）、图表单色 + 单强调、异常行语义 soft 底升权、日志回执（已于 2026-08-28 改为行内展开，见 Yunmu）。方向 B（暖纸回归）、C（仪表暗色）、D（画廊留白）保留为决策历史，不再约束实现。

### Locked Dashboard incident treatment · 2026-07-29

- 正常状态不显示独立“服务状态”区块，避免把健康遥测提升为永久信息层级。
- 当已观测请求错误率达到降级阈值时，在预算与消耗主内容中显示紧凑的 inline notice；文案遵循“事实 → 影响 → 一个动作”。
- inline notice 只负责当前异常与复查入口，持续记录进入全局 Status Center，不在 Dashboard 重复建设状态面板。
- 团队预算只读取已启用的 Spend Budget 配置，不得使用用户剩余额度与历史消耗反推预算。
- 团队聚合只在平台运营范围呈现；在后端提供真实 workspace 聚合接口前，客户工作区不得复用或冒充平台全局指标。

## Superseded baselines

以下方案保留为决策历史，不再约束新实现：

- Slate Signals / 雾灯
- Routing Ink / 路由墨
- Manrope Instruments
- Ink Fill、Signal Rail、Heavy Header、Fine Round、Mechanical Cut 与 Signal Scan 作为全局默认

Routing Ink 的选择板与旧审查稿已在决议固化后清理。

## Locked model status cards · C′ (2026-09-27)

Owner selected C′ and then approved the compact text treatment in local Claude
session `83ad64b0-5391-4173-989c-3d8910ecdc3e` (13:31 and 13:55 UTC).
Selected canvas: `archive:brand/canvases.md`.
See `foundations/yunmu-console.md`, “Locked model status cards”, for the
production contract, including the scoped healthy-label and stream-rate rules.

## Locked console overview + sessions (2026-09-30)

- **Canvas:** `archive:brand/canvases.md` (third page: overview A3, charts, palettes; fifth page: sessions)
- **Decision:** overview A3 on both planes, charts 墨 + 柱与线, default range 24 小时; sessions as a 会话 tab with a full-page detail
- **Rules:** `foundations/yunmu-console.md` → "Locked console overview"
- **Tokens:** `tokens/colors.css` `--lazu-chart-1/2/3` now carry the ink series (light and dark)

## Pending decisions

- Marketing brand language and imagery
- Remaining implementation details inherit current Yunmu tokens and the applicable locked foundations, including the September Console / Docs refinement

**Homepage implementation decision · 2026-07-12:** 首页纳入 Brand-to-Product 迁移，视觉遵循 `foundations/public-web-system.md`，产品叙事遵循 gateway-first 边界。历史首页探索板已清理，不得作为实现依据；智能路由不得重新成为首页主叙事。

公开网页系统已锁定：`foundations/public-web-system.md`。增强型 API 网关的角色、权限边界、10 项主要页面和 12 项次级页面结构均已锁定：`foundations/gateway-product-system.md`。智能路由降为未来基础设施占位，不进入当前主导航。以上决策的来源选择器与历史比较板（2026-07 explorations 批次）已在决策锁定后清理，决策结论以 `foundations/` 文档为准。

## Locked logo baseline

当前产品使用已锁定的 Seal mark（2026-09-20），并提供 primary / stacked / wordmark / icon 的 Graphite、Paper 与受限单色版本。所有产品和对外触点必须使用 `assets/logo/` 的正式资产，不得从旧探索板或页面私有实现中重建标志。Counterweight（2026-08-01）已退役，只作决策历史。

## Locked Console / Docs review · 2026-09-06

User approved the final `archive:brand/explorations/ui-redesign-2026-09/` prototypes for
production implementation. See `foundations/console-review-2026-09.md` for the
selected scope, exclusions, inline credential behavior and compact layouts.

## Locked UI audit fixes · 2026-09-09

用户为审计标记 小修即可 的十二个页面（归并为六组）各选定一套方案：01 A · 02 B ·
03 A · 04 A · 05 D · 06 D。约束见 `foundations/ui-audit-fixes-2026-09.md`；该文档
同时把模型目录单独纳入本次范围，此前 2026-09-06 review 将其排除。

- 模型运营页：模型账簿、来源 R6/R6b 与折扣矩阵 E1 的锁定约束见
  [model-ops-ledger](foundations/model-ops-ledger.md)。行点开居中弹窗，与系统其他编辑一致。

## Locked team projects · 2026-09-10

团队项目模型、付款人/发起人边界、成员 Key 权限和 Stripe Invoice 规则以
[team-projects-2026-09](foundations/team-projects-2026-09.md) 为准。它取代旧
team-console 探索稿的角色与后端待办，登记成员详情主从页例外；平台客户管理继续
独立保留。2026-09-10 修订：只有一种 Key（属于项目和一名成员），没有服务 Key、
负责人与交接；成员被移出或自行离开时其在该项目的全部 Key 立即停止，没有宽限期，
10 分钟撤销窗口不变；邀请链接有效期 3 天；每人每项目最多 200 把未删除的 Key（2026-09-29 由 10 放宽，只防脚本失控，不是产品规则）。
这四条是固定规则，不出现在项目设置里，也不可由用户配置。该决议不表示后端与 UI
已全部上线。

## Locked reasoning-effort cap + team refinement（2026-09-28）

- **Selected direction:** `A · 刻度`, drawn as `A2 · 填线`
- **Rules:** `foundations/team-projects-2026-09.md` § Reasoning-effort cap and team refinement
- **Visual reference:** `archive:brand/explorations/thinking-depth-2026-09/`（`python3 build.py`）

管理员给成员、成员的每把 Key 设「思考深度」上限：一把梯子
`minimal < low < medium < high < xhigh < max`，选一档即上限，之下全可用；三层只能收窄
（项目默认 → 成员 → Key）。超过上限的请求按上限执行、不拒绝，网关按各家支持的档位换算，
日志记「请求 → 执行」并计为「被降」。控件是六个等宽英文词 + 填到上限的墨线，不翻译。
同一决议把成员详情页换成 520px 居中小弹窗（预算 / 允许模型 / 思考深度），项目设置改用
个人设置同一套卡片行系统。

## Locked usage logs · M3 + L1 + S1（2026-09-29）

- **Selected direction:** list with `L1` lane tags, inline detail `M3`, official-price line `S1`
- **Rules:** `foundations/usage-logs-2026-09.md`
- **Visual reference:** design canvas `archive:brand/canvases.md`

用量与日志：线路（稳定 / Agent）作为标签挂在模型名后，不再有「分组」列；失败只标状态格，不涂整行。
点开一行在行下展开 M3 收据：左侧实扣 + 官方价与省下的钱，右侧时间条与费用条对齐，底部一行事实以请求 ID
收尾。官方价在计费时快照，只比输入 / 输出，没省就不说。上游模型只给平台视角；不提供「查看原始数据」。

## Locked e-mail system · C 分层（2026-09-30）

- **Selected direction:** `C · 分层`（墨头 · 纸面 · 运维）
- **Rules:** `foundations/emails.md`
- **Visual reference:** `archive:brand/explorations/emails-2026-09/`（`python3 build.py`）

每封邮件按「新闻是什么」归入三层：金额或比例用墨头（付款、余额调整、余额不足、Key 用量），账号与团队用纸面，
运维告警用只发管理员的等宽运维页。三层共用 640px 外框、卡片外页脚、一个墨色主按钮 + 一个文字链接。发给人的
邮件按收件人的控制台语言发中文或英文；付款邮件保持英文。收据 PDF、待付款提醒、通道测试完成等 5 种已删除，
后台不再编辑邮件模板。

## Locked payment e-mails · 墨头（2026-09-23）

- **Selected direction:** `方案二 · 墨头`（ink masthead）
- **Rules:** `foundations/payment-emails.md`
- **Visual reference:** `archive:brand/explorations/payment-emails-2026-09/`（`python3 build.py` 生成 `index.html`）

每笔付款成功的充值只发一封 Lazu 邮件（2026-09-27 由两封合并）：眉行 `PAYMENT RECEIVED`，主视觉是
到账金额，事实为付款时间、支付方式、钱包余额与 Invoice 编号，主按钮 Open wallet 加一个 View invoice
链接（落到 Stripe hosted Invoice 页，不分发写着 Amount due 的 Invoice PDF）。不区分个人与团队，永远写
「your Lazu account」。墨底头部 + 纸面正文，衬线只用于 ≥ 20px 的主视觉，眉行保持英文机器语域，页脚在
卡片外写 `Lazu.ai`。Stripe 自己的「成功的付款」邮件保持关闭。

## Locked SEO surfaces + 运营 (2026-09-29)

- **Approved canvas:** `archive:brand/canvases.md` (`archive:brand/explorations/seo-surfaces-2026-09/`)
- **Rules:** `foundations/seo-surfaces-2026-09.md`

Public header 模型 · 文档 · 博客 · 常见问题; four-column footer with the
DUBRIO LLC legal line; /about; API-rendered /models and one page per model
(layout A); blog with auto-drawn covers; docs under lazu.ai/docs. Console
「公告」 becomes 「运营」 (上新 · 公告 · 博客 · 配图) with a split Markdown /
live-preview blog editor.

## Locked Docs B workbench (2026-09-23)

Owner chose Docs direction `B · 工作台` (split workbench for endpoint pages,
single column elsewhere, dark ink code in both themes, `Copy page ▾` page
actions with a Copy-for-agent prompt, live Send request panel). Full rules:
`foundations/public-web-system.md` § Locked Docs B workbench. Supersedes the
light-code rule of the 2026-09-21 Docs board.

## Locked Seal continuation (2026-09-21)

Owner reconfirmed the approved Console / Docs canvas during PR #252 repair.
Console headings and display money extend the Seal serif; dense tables and
controls stay sans/mono, and Models retains its previous typography and status
colours. See `foundations/yunmu-console.md` § Locked Seal refinement for the
explicit exception to the public-only wording above. Docs request/receipt
composition is specified in `foundations/public-web-system.md` below.
