# Lazu console deepening — Quiet Precision

**Status: LOCKED**
**Locked: 2026-07-19**
**Current foundation:** [Yunmu](yunmu-console.md). The July decision below is provenance; current color and receipt rules include the August amendments.
**Approved direction:** `A · Kumo 精密 / Quiet Precision`（四页全选 A；历史选择板已在决议固化后清理）
**Canonical tokens:** `../tokens/ui.css`（新增 data-display 段）, `../tokens/colors.css`（无新增）

## Decision

2026-07-19，四个主要页面（首页、控制台 Dashboard、模型广场、用量与日志）的高级感方向统一选定 `A · Quiet Precision`。该方向完全处于 Kumo Native 已锁定边界内：不换色板、不换字体、不引入新组件语言。高级感来源是**秩序与克制**——更大的字阶反差、发丝线代替卡片、数据本身作为视觉主角。

## Rules

1. **去卡片化（de-carding）**：Dashboard / 用量页的 KPI 汇总不再使用独立卡片，改为同一表面上的分栏 + 竖向发丝线（`--lazu-border`）。卡片仅保留给真正独立的资源实体（模型卡、通道详情）。
2. **数据字阶**：KPI 主数字使用 `--lazu-display-kpi`（34px/700），汇总条数字使用 `--lazu-display-stat`（26px/700），均配 `-0.02em` 字距与 12–13px 的 `--lazu-text-faint` 标签。数字与标签的反差是页面的主要视觉节奏。
3. **表格数字一律 tabular-nums**：所有金额、token 数、延迟、百分比使用 `font-variant-numeric: tabular-nums`；金额与 ID 使用 mono（`--lazu-font-mono`），右对齐。
4. **图表单色 + 单强调**：数据可视化默认 `--lazu-chart-1`（G1 起为青 `#1F6F6B`）作唯一强调，辅线用 `--lazu-chart-2`/`--lazu-chart-3`；不引入多彩图例。图表无背景、无边框，仅底部一条 `--lazu-border` 基线。
5. **异常升权**：表格中的退款/失败/降级行使用语义 soft 色作整行底（退款与失败 `--lazu-danger-soft`，降级 `--lazu-warning-soft`），并保持文字/图标双通道表达；正常行不加底色。
6. **行内回执（inline receipt）**：日志/用量行点击在原行下方展开请求回执（RouteReceipt），不打开抽屉，回执代码块沿用 `--lazu-code-bg` 深底样式；回执是唯一允许的"深色区块"。
7. **筛选 chips**：列表页筛选用 pill chips（`--lazu-radius-pill`），选中态为 `--lazu-ink` 实底白字，未选中为 hairline 描边；数值筛选（如价格上限）选中时可用 `--lazu-action` 文字色。
8. **公开首页**：沿用 `public-web-system.md`，深化仅体现在：mono 眉题 + 大字阶标题 + 回执代码块作为 hero 证据 + 发丝线分隔的事实条（stats strip）。
9. **留白优先于分隔**：区块间优先用间距（`--lazu-space-6/8`）与单条水平发丝线，不叠加多重边框、底色、阴影。阴影规则不变（仅真实浮层）。

## Non-goals

- 不引入暗色仪表风（方向 C）与画廊极简（方向 D）的任何全局规则；C 的"异常升权"已以规则 5 的浅色形式吸收。
- 不改变已锁定的色值、字体、图标、动效 token。
- 不新增组件语言；全部规则通过 `apps/web/src/components/ui/` 现有组件的样式深化实现。

## Historical rollout order (July 2026)

1. Dashboard（KPI 去卡片化 + 图表单色）
2. 用量与日志（汇总条 + tabular-nums + 异常行；回执后来改为行内展开）
3. 模型广场（表格化 + 能力 chips + 价格 mono 右对齐）
4. 公开首页（字阶与事实条微调，叙事不变）

## Seal continuation (2026-09-21)

The owner-approved Console exception is defined by
[yunmu-console.md](yunmu-console.md#locked-seal-refinement-2026-09-21):
page headings and display money use the display serif, while Models, dense
tables, controls and non-money figures retain their existing typography.
This explicit amendment takes precedence over the earlier public-only /
bold-money wording above.
