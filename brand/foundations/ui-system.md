# Product UI system — Yunmu

**Status: LOCKED**
**Locked: 2026-07-11**
**Current direction:** [Yunmu](yunmu-console.md), with [September refinements](console-review-2026-09.md).
Color, overlay, row and state rules below incorporate later locked amendments.

## Intent

Lazu 的界面是一套现代开发者工作台：复杂能力在结构上清楚，在视觉上保持温和。系统吸收 Kumo 的 page header、sidebar、resource list、semantic surface 和 command palette 语法，同时保留 Lazu 的路由、成本、延迟与可靠性语义。

专业感来自信息结构、任务连续性和一致组件，颜色与交互按现行云母规则表达，避免装饰性重表头或过细笔触。

## Locked decisions

| Area           | Locked direction                       | Implementation rule                                                                                          |
| -------------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| Typography     | **Geist Utility**                      | Geist 负责拉丁与数字；中文使用 system CJK；代码与 ID 使用 IBM Plex Mono / system mono。                      |
| Type hierarchy | **Large Page Header**                  | 页面标题 28–32px；正文与控件不低于 12px 的生产等效尺寸；标签只在真正的元信息中缩小。                         |
| Density        | **Calm Chrome + Efficient Data**       | 导航、标题和设置保持呼吸；表格、日志与指标局部紧凑，不使用全局单一密度。                                     |
| Shape          | **Kumo 7**                             | 默认控件和表面 7px；浮层 10–12px；Badge 5px；pill 只用于筛选、状态和短标签。                                 |
| Surface        | **Semantic Flat Layers**               | page / base / subtle / selected / overlay 分层；默认 hairline，无卡片墙与大面积阴影。                        |
| Buttons        | **Brand Primary**                      | 同一任务区域最多一个墨/纸反转主操作；secondary 使用白色/主题表面加边框；ghost 只用于高密工具栏。                          |
| Focus          | **Action Ring**                        | focus-visible 使用云母证据色焦点，数值消费 canonical tokens；错误态必须同时有文字解释。                                 |
| Navigation     | **Neutral Fill**                       | 选中项使用中性灰填充与较重文字；选中只使用底色、字重与前景色，不使用 Signal Rail。                           |
| Page header    | **Task Header**                        | 标题、说明、范围和主要动作组成稳定页面头；设置靠近受影响资源，不集中到远离任务的位置。                       |
| Tables         | **Subtle Header**                      | 表头使用 subtle surface 和 muted text，不再使用深色 Heavy Header；行间距消费 Yunmu 的 row tokens，hover 使用 selected surface。 |
| Status         | **Shape + Text + Color**               | Healthy=圆、Degraded=菱形/三角、Down=叉；状态颜色不能单独承担意义。                                          |
| Charts         | **Action Evidence + Muted Categories** | 青（`--lazu-chart-1`，G1）表达主证据，中性灰表达辅助序列；面积用于用量/成本；分类色只识别模型与供应商。                          |
| Icons          | **Medium Round / Phosphor-like**       | 约 1.5px 笔触、圆角、16px 仍清楚；默认描边，选中或关键对象可局部填充。                                       |
| Motion         | **Direct Ease**                        | 常规 120–180ms，短淡入与方向移动；不使用弹跳，也不再以 Mechanical Cut 作为品牌特征。                         |
| Empty/error    | **Structured Empty**                   | 遵循 paper-states.md；高频状态零装饰，标题、原因与恢复动作按需要出现。                               |
| Overlays       | **Contextual Layer**                   | 快速选择用 Popover/Command Palette；编辑用居中 Dialog；单行只读回执原位展开；客户台账保留已锁定的操作抽屉例外。                       |
| Code           | **Dark Code Island**                   | 代码、JSON、curl 与长日志使用中性深色独立表面，不把整个产品主题变成终端。                                    |
| Selection      | **Soft Standard**                      | Checkbox/Radio/Switch 遵循熟悉语义、标准键盘行为和清楚 focus。                                               |
| Tabs           | **Action Underline**                   | 同级页面使用墨色 underline；2–4 个互斥模式可使用 neutral segmented control。                              |
| Loading        | **Skeleton + Real Progress**           | 列表用 skeleton；真实进度使用 progress/steps；不再默认使用持续 Signal Scan。                                 |
| Copy           | **Instrument**                         | 事实 → 影响 → 一个动作；保持简短、具体、无拟人和营销腔。                                                     |

## Page composition

- 标准侧栏 244px，包含 workspace switcher、全局搜索、任务分组导航和账户区。
- 页面标题与说明形成稳定 header；主要操作位于右侧，同一区域最多一个 primary。
- 概览指标可以共享一个带分隔线的 surface，避免四张悬浮 KPI 卡。
- 设置项与受影响资源保持相邻；复杂产品 IA 以用户任务命名，而不是内部产品名堆叠。
- 资源列表优先于营销式卡片网格；只有独立对象需要浏览和比较时才使用卡片。
- Command Palette 同时承担跳转、搜索资源和运行高频动作，但不能隐藏页面的基本导航。

## Chart grammar

- **Primary evidence:** `--lazu-chart-1`（G1 起为青），用于当前页面最重要的一条趋势或选中序列。
- **Secondary evidence:** 中性灰，用于对比序列、目标线或历史基线。
- **Area:** 只用于 Token、请求量、成本与缓存节省等体量信息，透明度 10–14%。
- **Categories:** 模型与供应商使用低到中饱和分类色；分类色永远不表达健康。
- 网格线使用 semantic hairline；图表必须支持键盘焦点、文本摘要和数据导出。

## Exceptions

- Lazu Logo 已锁定为 Seal（2026-09-20）；主题配对、尺寸与使用限制以 `logo-system.md` 为准，Kumo UI 不得重绘或改变其几何。
- 营销站可以增大字号和留白，但必须延续云母色彩、Geist 字体层级和墨/纸主操作。
- `Routing Ink`、`Manrope Instruments`、`Heavy Header`、`Signal Rail` 与 `Mechanical Cut` 已归档，不再约束新实现。

Model operations follows [model-ops-ledger](model-ops-ledger.md): a ledger whose rows open a centred dialog.
