# Public web system — Yunmu / Mono Utility

**Status: LOCKED**
**Locked: 2026-07-11**
**Amended: 2026-09-21** — 公开页其余各页落地 Seal：FAQ 答案常开（第 33 条修订）、条款页与 Key 查询改纸面单栏、`/key` 与状态页归公开顶栏。决策板见 `archive:brand/explorations/brand-refresh-2026-09/` 的第二块画布。
**Amended: 2026-09-20 (b)** — Hero 恢复控制台窗（成员与额度），display 阶随之收到 42–58px；第 03 / 16 条已按此改写。决策板 `archive:brand/explorations/brand-refresh-2026-09/` 的 `C · 窗`。
**Amended: 2026-09-20** — 随 `Seal` 标志决议，display 字体改为 Instrument Serif（`--lazu-font-display`），首页 Hero 改为编辑式单栏；第 01 / 03 / 16 / 17 / 19 / 20 条已按此改写。
**Amended: 2026-08-20** — 颜色规则改为跟随 `02 云母 Yunmu`（见 `yunmu-console.md`）。
公开页不再有自己的颜色层：`--lazu-web-landing-*` 已从 `tokens/public-web.css` 删除，
落地页与登录页直接消费 `--lazu-*`。第 26 / 27 / 31 / 32 条已按此改写。
**Decision provenance:** historical selection board removed after the rules below were locked
**Canonical tokens:** `../tokens/public-web.css`

## Intent

Lazu 的公开网页不是另一套营销皮肤，而是 云母产品系统面向访客的表达层。它使用更宽的字号、更舒展的阅读节奏和更明确的品牌时刻，但继续遵守暖中性色、单一墨/纸主动作、Geist/IBM Plex Mono、Kumo 7 与 semantic surfaces。

公开网页的识别点是 **衬线声明式标题 × 一张看得懂的账 × 温和的任务型结构**。display 标题与标题旁的大数字使用字标的衬线（Instrument Serif），等宽字体退到眉题、编号、ID 与机器对象，两者都不用于正文。

## 1. Visual foundations

| #   | Area             | Locked direction | Implementation rule                                                                                 |
| --- | ---------------- | ---------------- | --------------------------------------------------------------------------------------------------- |
| 01  | Display headings | **衬线声明式**   | Hero 主声明、章节标题与标题旁的大数字使用 `--lazu-font-display`（Instrument Serif；CJK 退回系统宋体）；眉题、编号与 ID 用 IBM Plex Mono；20px 以下一律不用衬线。 |
| 02  | Reading          | **舒展阅读**     | 正文 17px / 1.75，单栏阅读宽度约 720px；长文不铺满 1280px 容器。                                    |
| 03  | Type scale       | **宽屏表达阶**   | Display 42–58px（衬线，行高 1.02，字距 -0.015em；与右栏说明并排，不独占整行；下限 42px 满足 <480px 的 Display 底线），H2 30–46px（衬线），H3 21px（Geist 600）；响应式使用 `clamp()`。 |
| 04  | Spacing          | **4/8 自适应阶** | 组件内部坚持 4/8 倍数；页面级间距允许 24/48/72/96/120 形成节拍。                                    |
| 05  | Grid             | **1280 十二列**  | 内容最大宽度 1280px，12 列、24px gutter；长文和表单使用子网格收窄。                                 |
| 06  | Section rhythm   | **章节留白**     | 主章节间距 72–120px；相关子区块使用 48–80px，不用连续卡片填满页面。                                 |
| 07  | Radius           | **Kumo 7**       | 控件与普通表面 7px，浮层 11px；pill 仅用于悬浮公告、短状态与筛选。                                  |
| 08  | Surface          | **无框色差**     | 默认依靠 page/base/subtle 的色差分区；hairline 只在边界、数据和交互需要时出现，阴影只用于真实浮层。 |

## 2. Shared public chrome

| #   | Area              | Locked direction  | Implementation rule                                                                     |
| --- | ----------------- | ----------------- | --------------------------------------------------------------------------------------- |
| 09  | Announcement      | **悬浮胶囊**      | 用于发布、活动和非阻断更新；居中悬浮、可关闭，不挤压 Header。维护和故障仍使用正式通知。 |
| 10  | Header            | **Kumo 标准顶栏** | 单层 64px 顶栏：Logo、主要导航、低频全局控件、登录和唯一主 CTA。                        |
| 11  | Active navigation | **Neutral Fill**  | 当前项使用中性填充与较重文字；橙色不铺满导航项。                                        |
| 12  | Global controls   | **独立图标**      | 语言、主题和账户保持独立、带 tooltip 的图标按钮；不可只依赖图形含义。                   |
| 13  | Mobile navigation | **底部 Sheet**    | 小屏保留 Logo 与主要 CTA，完整导航从底部 Sheet 展开；支持拖拽关闭、ESC 和焦点锁定。     |
| 14  | Page heading      | **任务页头**      | Breadcrumb、标题、摘要和相关动作构成稳定二级页头；避免纯装饰封面。                      |
| 15  | Footer            | **结构化四栏**    | 产品、开发者、公司、法律/状态四组；底部只留品牌归属与版权行。语言和主题只在 Header（2026-09-30）。 |

## 3. Marketing and proof modules

| #   | Area              | Locked direction | Implementation rule                                                                  |
| --- | ----------------- | ---------------- | ------------------------------------------------------------------------------------ |
| 16  | Homepage Hero     | **声明 + 控制台窗** | 1160 容器：眉题；衬线主声明（末句斜体强调）在左，说明句与两个动作在右 380px 栏；下方 880px 控制台窗（窗条 + 侧栏 + 成员与额度页），随滚动由后仰归位；再下是一条墨线与线下三个衬线数字。窗内是按真实控制台绘制的界面，跟随访客语言，不嵌截图、不放装饰性伪终端。 |
| 17  | Providers         | **线下三个数字** | 模型数、供应商数、当前可用率来自 `/api/pricing` 与状态接口，取不到就整条隐藏；不堆 Logo、不写死数字。 |
| 18  | Core capabilities | **能力工作台**   | Route/Observe/Control 以证据、操作和结果组成局部工作台，避免同构营销卡片墙。         |
| 19  | Proof modules     | **账本条目**     | 「四个头疼」是一份编号账本：衬线编号、标题与说明在左，真实控制台面（模型价目、额度、Key、月账）在右，全部用发丝线分区，不用卡片。 |
| 20  | How it works      | **三行编号**     | 注册并充值 → 配置 → 建 Key，三列并排、衬线编号、一条墨线起头；不做卡片墙。 |
| 21  | Use cases         | **任务清单**     | 按任务和目标组织场景，不按模糊人物画像组织；每项指向具体能力或指南。                 |
| 22  | Architecture      | **Kumo Flow**    | 客户端、Lazu、供应商和观测层使用清晰节点流；连线表达方向，不做装饰拓扑。             |
| 23  | Quickstart        | **代码 + 回执**  | 示例旁必须显示响应、模型、路由、延迟或成本回执，让接入结果可验证。                   |
| 24  | Pricing           | **价格卡片**     | 用少量清楚方案卡解释开源、云与企业路径；复杂能力差异另用比较表。                     |
| 25  | Final CTA         | **Kumo 行动带**  | 页面末尾使用短标题、一个墨/纸主动作和一个中性次动作，不重复整页销售叙事。             |

## 4. Actions, forms and discovery

| #   | Area           | Locked direction  | Implementation rule                                                                                                                                                                                                                                                                                                                                                                                                          |
| --- | -------------- | ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 26  | Buttons        | **墨/纸 primary** | 主按钮为墨底纸字（深色下纸底墨字），带边框 secondary、无框 ghost、语义 danger；同一区域仅一个 primary。暖橙已随 BRAND.md § 02 退役。                                                                                                                                                                                                                                                                                         |
| 27  | Links          | **证据色链接**    | 可交互文字使用证据色（G1 起为青）；正文链接仍需下划线或上下文动作，外链标记 `↗`。                                                                                                                                                                                                                                                                                                                                             |
| 28  | Badge and tag  | **Kumo 方角**     | 5px 小圆角；版本、分类、Beta 与状态分开建模，状态继续使用形状 + 文字 + 颜色。                                                                                                                                                                                                                                                                                                                                                |
| 29  | Forms          | **字段 + 说明**   | 标签在上，帮助和影响说明与字段相邻；验证信息说明原因并给出恢复动作。                                                                                                                                                                                                                                                                                                                                                         |
| 30  | Search         | **命令面板**      | `⌘K` 同时支持站点跳转、模型/文档搜索与高频动作；基本导航不能因此被隐藏。                                                                                                                                                                                                                                                                                                                                                     |
| 31  | Authentication | **落纸单门**      | 登录注册是同一扇门，352px 单列直接落在云母地面上，不套卡片、不加投影。列首是横版字标，不设可见标题——凭据分段与主按钮已经说明这一屏在做什么。两种凭据（邮箱验证码、密码）由分隔线下的分段控件切换，任何时刻只有一种在屏上；只剩一条路时分段整体消失，绝不留残缺控件。条款是主按钮下方的脚注，不是勾选框。不出现与步骤数不符的进度条，不放不承载信息的插画。SSO 只在后端确实配置了对应供应商时出现，并使用该供应商自己的标记。 |

## 5. Content and data components

| #   | Area           | Locked direction     | Implementation rule                                                                  |
| --- | -------------- | -------------------- | ------------------------------------------------------------------------------------ |
| 32  | Tabs           | **Action Underline** | 页面同级视图使用墨色下划线；互斥显示模式仍可使用 neutral segmented。                 |
| 33  | FAQ            | **分组问答**         | 按真实主题分组；每个问题带锚点，答案常开。折叠会把答案藏在一次点击后面——读者要找的就是答案，而爬虫不会点。（2026-09-20 修订：原「单项展开」条款退役。） |
| 34  | Resource cards | **身份条目**         | 每个资源突出类型、标题、摘要和唯一动作；优先条目/列表结构，不默认做悬浮卡片。        |
| 35  | Public tables  | **Subtle Header**    | 表头使用 subtle surface 与 muted text；48px 行高，支持排序、窄屏横向滚动和文本摘要。 |
| 40  | Pagination     | **数字分页**         | 明确当前页、总页数和前后动作；数据列表保留 URL 状态，避免刷新后丢失位置。            |

## 6. Feedback and recovery

| #   | Area                | Locked direction | Implementation rule                                                               |
| --- | ------------------- | ---------------- | --------------------------------------------------------------------------------- |
| 36  | Callout and banner  | **时间线通知**   | 更新、维护和弃用附时间、影响范围与下一步；按时间顺序呈现时不混用普通营销 Banner。 |
| 37  | Overlays            | **侧边 Sheet**   | 详情、配置和长任务从右侧 Sheet 展开；确认式、不可逆动作继续使用居中 Dialog。      |
| 38  | Toast               | **状态中心**     | 短 Toast 只作即时回执；后台任务和持续通知汇入可再次打开的状态中心。               |
| 39  | Empty/loading/error | **恢复任务态**   | 说明事实、影响和恢复动作；局部失败不伪装成全页错误，真实进度必须显示真实数值。    |

## Composition rules

1. 衬线是品牌声明，不是正文默认。一个 viewport 内只设置一个主要衬线声明焦点；Mono 退到眉题、编号与机器对象。
2. 无框色差不等于没有边界；表格、表单、焦点和浮层仍必须提供足够对比。
3. 公开网页复用产品的语义和组件行为，但允许更大的字体、更宽的留白与更强的代码证据。
4. 实时路由、成本、延迟和可靠性数字只有在有口径或真实数据源时才可使用；示意稿必须标记 demo。
5. 每个模块只保留一个主要动作。命令面板、Side Sheet 和状态中心是增强入口，不取代基础导航与页面内反馈。

## Responsive baseline

- `>= 1280px`：完整 12 列；Hero 和代码证据可分屏。
- `768–1279px`：保持 8 或 12 列逻辑，容器边距缩小；指标带允许 2×2。
- `< 768px`：单列内容；Header 导航进入底部 Sheet；表格横向滚动；Side Sheet 宽度不超过 92vw。
- `< 480px`：Display 最低 42px；CTA 可纵向排列；悬浮公告不得覆盖主要导航和浏览器安全区。

## Accessibility baseline

- 正文和控件不低于 WCAG AA 对比；品牌橙色文本必须在实际背景上单独校验。
- 所有独立图标控件有可读名称和 tooltip；Command Palette、Sheet、Accordion、Tabs 与 Pagination 支持键盘。
- `prefers-reduced-motion` 下移除非必要移动，保留状态变化和即时反馈。
- 状态、价格与图表不得只靠颜色；代码示例提供复制成功回执和可读文本。

## Locked Docs request and receipt workbench (2026-09-21)

Owner-approved final Docs board from the Seal canvas, reconfirmed for PR #252.
Endpoint pages keep the existing language tabs, samples, request controls and
responsive rail. Method/path precede the serif title; a billing line links to
the localized price reference and describes the endpoint's billing dimensions
without promising a fixed rate. Parameter rows retain the existing field,
requirement and explanation structure.

The request rail includes a receipt with request ID, model, lane, usage, elapsed
time, key label and a display-money total. An illustrative receipt must be
visibly marked as an example, with no claim that its amount is a current price
or the result of the reader's live request. Endpoints without a metered model
must not inherit a chat token charge. Studio is linked only for supported
interactive endpoints. Guides, API reference and Changelog remain distinct
navigation entries. On narrow screens the workbench follows the article;
no fixed-width panel may overflow the viewport.

~~Light code blocks and their overflow fade share `--lazu-surface-solid`;~~
superseded 2026-09-23 by the Docs B workbench below: every docs code block is
dark ink (`--lazu-code-bg`) in both themes. No private page colour replaces
these tokens.

## Locked Docs B workbench (2026-09-23)

Owner-approved Docs direction `B · 工作台 Bench`, chosen from four canvases
(A 纸本 / B 工作台 / C 账本 / D 双读者) with the refinements agreed in review.
Implementation: `apps/docs` (`public/styles.css`, `public/agent-actions.js`,
`components/endpoint-rail.tsx`, `components/docs-home.tsx`).

- **Chrome.** 56px top bar across the full width: Primary lockup + `DOCS`
  mono tag, Guides / API reference / Changelog, a centred 380px search
  ("Search docs or paste an error code"), language, theme and the ink
  Console button. A flat 244px sidebar with a hairline right edge; mono
  uppercase group labels; selection is fill + weight + foreground only.
- **Two page shapes.** Only pages that can send a request (the endpoint
  reference) are split: article on paper at left, a full-height graphite
  workbench column (`--lazu-code-bg`, ~39vw) at right. Everything else
  (guides, lookups, changelog) is a single 720px column with inline code
  and a right outline.
- **Type.** H1 is the display serif at 52px (home 68px); section headings
  are Geist 22/600 — the docs exception to "章节标题用衬线", because the
  reference is scanned, not read. Body Geist 15/1.7 at weight 400; mono for
  eyebrows, fields, types and code.
- **Code.** Dark ink on warm paper in both themes; Shiki paints with the
  `--lazu-code-*` tokens (`lazu-ink` theme). Every block has a dark header
  row (language, tabs or title) with the copy control; long lines fade out
  at the right edge instead of drawing a scrollbar.
- **Workbench.** Top to bottom: send panel (key · model · Send request),
  request in cURL / Python / TypeScript with line numbers, response, receipt.
  The key lives only in the tab (sessionStorage); the model picker reads the
  public catalog and preselects the docs default model. Before a send the
  response and receipt are labelled examples; after a send both show the
  reader's own request, read from the body and the `X-Lazu-*` headers, with
  a link to that request in the console.
- **Page actions.** One split button, `[ Copy page | ▾ ]`: the face copies
  the page as Markdown; the menu holds Copy (Markdown · Copy for agent) and
  Open in (ChatGPT · Claude · Gemini, which copies the prompt first because
  it has no prefill). "Copy for agent" is links + a prompt that states
  base_url, env-var auth and catalog model IDs and ends at "My task:". No
  "View as Markdown" item; the `.md` URL stays for agents and search.
- **Headings.** Every h2/h3 carries a focusable link control that copies the
  section URL; the ink toast confirms it.
- **Machine layer.** Each route ships a `.md` sibling with front matter
  (title, description, source, updated), served as `text/markdown`; pages
  link it with `rel="alternate" type="text/markdown"`, carry hreflang for
  every locale plus x-default, and answer on their slashless canonical URL
  without a redirect.
- **Method tags** on endpoint routes use success green for POST and evidence
  blue for GET, as drawn on the approved board.
