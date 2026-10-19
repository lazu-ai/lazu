# Feedback System — 纸上批注 + 墨条 Toast

> **Locked:** 2026-08-28
> **Approved exploration:** `lazu-ops:brand/explorations/feedback-2026-08/feedback-4ways.html`
> **Decision:** inline / banner / 表单 / 公告条 = **方案 A 纸上批注**；toast = **方案 C 墨条**
> **Inherits:** `yunmu-console.md`（地面、墨/纸反转、证据色）、`color-system.md`（语义色只表达状态）

这是云母体系下所有「错误、警告、成功、信息」的唯一规范。它取代此前并存的
`lazu-banner` / `lazu-inline-notice` 双组件、`public-status-alert` 硬编码色、
`dub-docs-callout` 左粗边，以及未成形的 `app-toast`。

---

## 1. 一条铁律：纸面不变色

页面里的提示长在纸上，不是长在色块上。**除 toast 外，任何反馈组件的背景都保持
`--lazu-surface-raised`，边框保持 hairline**。

> G1 修订（2026-10-06）：浅色下提示的边改为 1px 墨（`--lazu-notice-edge`），让它读作
> 一张放在页面上的便条，而不是又一张卡片；深色保持原 hairline。底色与「语义只由图标
> 和标题字色承载」不变。语义只由三样东西承载：

1. **图标**（形状即语义，色盲可辨）
2. **标题字色**（error / warning 染色；success / info 不染，避免正常状态喧哗）

> 此前这里还有第三项「必要时的一枚软底徽记（仅错误空态的图标框）」。整块状态面
> （加载 / 空态 / 行内错误 / 404 / 403 / 崩溃）已于 2026-09-01 改由
> `paper-states.md` 规范，图标框随之废除；本文档从此只管**页面里的提示**。

禁止：语义色大面积填充、语义色描边整个容器、左粗边强调条、虚线作为语义信号、
发光与阴影表达严重性。

> 为什么不是「淡色底」流派：云母的整套材质语法建立在「一张暖纸」上。往纸上刷一块
> 12% 的红，纸就破了。识别度靠图标 + 字色已经足够，这也是 Vercel Note / shadcn
> Alert 在 2025 年后成为开发者产品主流的原因。

## 2. Toast 是唯一的例外：墨/纸反转

toast 不是页面的一部分，它是浮在页面之上的**回执**。它复用品牌最强的资产——主按钮
的墨/纸反转：

- 浅色主题：墨底 `--lazu-toast-bg` `#202329` + 纸字 `--lazu-toast-fg`
- 深色主题：纸底 `#f1f1f2` + 墨字（反转的反转）
- 语义色**只**出现在 16px 图标上，并使用为反转地面提亮/压暗过的
  `--lazu-toast-*` 系列，不直接用 `--lazu-danger` 等页面语义色
- 圆角 `--lazu-radius-toast` 10px，比页面控件（12px）更紧，让它读起来像浮层而非纸片
- 阴影 `--lazu-shadow-toast`，不发光

toast 内的动作按钮使用 `--lazu-toast-control` 半透明填充，不描边、不用品牌色。

## 3. 组件与职责

| 组件              | 场景                               | 生命周期                               | role                                          |
| ----------------- | ---------------------------------- | -------------------------------------- | --------------------------------------------- |
| **Toast**         | 瞬时结果回执（保存成功、请求失败） | 自动消失，见 §6                        | `status` / 错误 `alert`                       |
| **InlineNotice**  | 卡片或区块内的持续状态             | 随底层状态出现与消失，**不给关闭按钮** | info/success `status`，warning/danger `alert` |
| **页面级 Banner** | 账号 / 计费 / 配额类待办           | 处理完自动消失                         | `alert`                                       |
| **FieldMessage**  | 表单字段校验                       | 随校验状态                             | 由 `aria-describedby` 关联                    |
| **ErrorState**    | 数据加载失败的整块缺省             | 直到重试成功                           | `alert`（外观见 `paper-states.md` §5）        |
| **公告条**        | 官网 / Status 全站通知             | 运营控制                               | `status`                                      |

`Banner` 与 `InlineNotice` **是同一个组件**：`Banner` 只是历史别名，内部委托给
`InlineNotice`，不再有第二套 CSS。新代码一律用 `InlineNotice`。

不允许再出现的做法：

- 用 toast 承载「必须处理」的错误（余额不足、密钥失效）——那是 Banner 的活
- 用多条 toast 叙述一个异步流程——只在终点发一条
- 页面各自用 Tailwind 任意值拼 alert（`border-[var(--ui-danger)]` 这类）

## 4. 语义色

四个语义色是唯一合法来源，**禁止任何硬编码 hex**：

| 语义    | Light                      | Dark      | 图标       |
| ------- | -------------------------- | --------- | ---------- |
| danger  | `--lazu-danger` `#b8443a`  | `#f06f62` | 圆形感叹号 |
| warning | `--lazu-warning` `#b3821b` | `#d8a53c` | 三角感叹号 |
| success | `--lazu-success` `#3f7957` | `#7ed3a7` | 圆形对勾   |
| info    | `--lazu-info` `#4c78a0`    | `#7ea0be` | 圆形 i     |

`--lazu-info` 是 2026-08-28 新增的独立语义色。此前 `--ui-info` 借用图表色
`--lazu-data-blue`，导致「改一次图表配色顺手改掉全站信息提示」。图表色与状态色从此
彻底分家。

软底 `--lazu-*-soft` 只用于 `aria-invalid` 输入框的 focus ring。不用于任何容器背景。
（错误空态的图标框曾是第二处，已随 `paper-states.md` 废除。）

## 5. 度量

| 项         | 值                           | token                  |
| ---------- | ---------------------------- | ---------------------- |
| 通知圆角   | 12px                         | `--lazu-radius-notice` |
| toast 圆角 | 10px                         | `--lazu-radius-toast`  |
| 图标尺寸   | 16px（全部场景统一）         | `--lazu-notice-icon`   |
| 标题       | 13px / 650                   | —                      |
| 正文       | 12.5px / `--lazu-text-muted` | —                      |
| 内边距     | 12px 14px                    | —                      |

图标尺寸此前是 16 / 17 / 20 / 22 四种，现在只有 16px 一种，没有例外容器。

## 6. 时长与位置

- 位置：**右下角**（移动端下居中）。sonner 默认，且不遮挡顶部导航与页面主操作。
- 成功 / 信息：4s
- 警告：8s
- 错误：8s，并始终带关闭按钮（可读、可关、不常驻累积）
- 公告类 `notice`：常驻，由用户关闭

错误 toast 走去重（4s 内同文案不重复）与限流（2.2s 内最多 2 条）。**warning /
success / info 同样走位置适配**，此前只有 error 分支适配移动端是 bug。

## 7. 文案

沿用 Instrument 规则：**事实 → 影响 → 一个动作**。

- 标题是事实，不是感叹（「路由已停用」不是「出错了！」）
- 正文一句话说清影响
- 最多一个主动作，动作用动词开头（「检查密钥」不是「点此查看」）
- 不出现堆栈、不出现裸 HTTP 状态码；错误码只在需要上报时出现

## 8. 可访问性

- 错误：`role="alert"`（打断播报）；其余：`role="status"`
- 本规范内的反馈提示必须同时有图标与文字，不能只有颜色；整块状态面遵循
  `paper-states.md` 的小圈 / 虚线位 / 断线词汇
- 表单错误：输入框 `aria-invalid="true"` + `aria-describedby` 指向消息节点
- toast 区域 `aria-live="polite"`
- 提示文字与其地面的对比度满足 WCAG AA

## 9. 落地清单（2026-08-28 随本规范一并完成）

- `tokens/colors.css` 新增 `--lazu-info` / `--lazu-info-soft` / `--lazu-toast-*`
- `tokens/ui.css` 新增 `--lazu-radius-notice` / `--lazu-radius-toast` /
  `--lazu-notice-icon` / `--lazu-shadow-toast`
- `Banner` 委托给 `InlineNotice`，`.lazu-banner` 的重复 CSS 删除
- `Input` 支持 `error` prop 并透传 `aria-invalid`（此前表单错误态输入框永不变红）
- `ErrorState` 与 `EmptyState` 拆分；错误态使用 `paper-states.md` 锁定的断线 + danger 点，
  空态使用虚线位
- `Modal.error` / `Modal.warning` 恢复语义 tone（此前与普通确认框无差异）
- Health 页与 Status 页的硬编码红/黄/绿全部换成语义 token（`header-controls.css`
  的 `#d97706` 是主题切换器里太阳图标的装饰色，不表达状态，不在本规范管辖内）
- docs 站 callout 从左粗边改为纸上批注，与主产品同语言
