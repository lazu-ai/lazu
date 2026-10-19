# Lazu Logo System — Seal

> **Status:** LOCKED · 2026-09-20
> **Selected direction:** `C · 印章 / Seal` + wordmark `W1 · Instrument Serif`
> **Supersedes:** `E · 配重方柱 / Counterweight`（2026-08-01）
> **Decision history:** `lazu-ops:brand/explorations/brand-refresh-2026-09/`
> **Canonical assets:** `brand/assets/logo/current/`

## 1. Core mark

Lazu 的正式标志是一枚圆形印章：圆容器里沉在左下的几何 `L`，右上一个圆点。
L 是纸上的一笔，圆点是印在纸上的一点朱砂位——正式件里它与 L 同色，只有
PWA / 通知这类需要状态的触点可以把圆点换成语义色（见 § 3）。

标志的核心几何固定为 64 × 64 网格：

- 容器：圆，圆心 `(32, 32)`，半径 32。
- `L`：从 `(18, 17)` 起，竖笔宽 10、高 30；底部横笔高 10，延伸至 `x = 46`。
- 圆点：圆心 `(41, 23)`，半径 5。
- 1024 画布按 16 倍缩放，不做任何视觉修正。

不得拉伸、旋转、描边化、加渐变、加投影，或改变 `L` 与圆点的相对位置。圆点不得
变成方块、不得与 `L` 相连、不得增加为多个。

## 2. Wordmark

字标是 **Instrument Serif Regular** 排出的首字母大写 `Lazu`，L–a 之间手动收
30/1000 em，其余保持原字距。它是四个字母里唯一有「运动」的地方——`z` 的对角线
从粗到细，像一笔写出来的；标志的几何 `L` 站着不动，字标的 `z` 在走。这是标志与
字标之间刻意的反差：标志是印，字标是签名。

- 正式字标只有 `Lazu` 一种写法：首字母大写、不加斜体、不加下划线或圆点。
- 字标永远来自 `brand/assets/logo/current/lazu-wordmark-*.svg`（路径化轮廓），
  不得用网页字体临时排出来替代锁定件；`extract-wordmark.py` 是唯一的路径来源。
- 斜体只给公开页 hero 的强调词与 404 页，不进锁定件。
- 字标最小可见高度（cap height）**17px**。低于此高度只用 Icon，不缩小字标。

## 3. Theme pairing

线上产品必须使用带容器的主题版本：

| 背景环境    | 使用版本     | 容器      | 图形与字标                     |
| ----------- | ------------ | --------- | ------------------------------ |
| 浅色 / 默认 | **Graphite** | `#20201d` | `#f8f8f6` 图形；`#20201d` 字标 |
| 暗色        | **Paper**    | `#f8f8f6` | `#20201d` 图形；`#f8f8f6` 字标 |

默认资产是 Graphite：即深色容器、纸白图形。暗色主题必须切换为 Paper：即纸白容器、
深色图形，并带 3/1024 的石墨 10% 描边（16px 下不足半像素，读作发丝线）以免在浅灰面上失去边缘。选择依据是标志所在
的实际背景，而不是文件名或操作系统主题本身。

状态点（仅 PWA 角标、系统通知、状态页）：`over cap` 用 `--lazu-danger`，`syncing`
用 `--lazu-evidence`，`healthy` 用 `--lazu-success`。网站、控制台与文档里的标志
永远不带状态点。

单色无容器版本（`lazu-icon-flat-*`）把容器画成 3 单位圆环，只允许用于单色印刷、
压印、蚀刻、刺绣或明确要求单色的第三方平台；不得作为网站、Docs、控制台、PWA 或
浏览器图标的常规版本。

## 4. Approved lockups

- **Icon**：印章；用于 favicon、应用图标、紧凑导航。
- **Primary**：Icon + `Lazu` 字标，字标 cap height 为容器直径的 55%，基线落在容器
  中线下 23/80；用于页头、侧栏、登录、邮件与横向品牌位。
- **Stacked**：Icon 位于字标上方；用于方形或居中展示与社交分享图。
- **Wordmark**：仅 `Lazu`；只在图标已在同一视觉区域明确出现时使用。

控制台侧栏、页脚与 Studio 顶栏一律使用 Primary（30 / 28px 高），不再用 Icon 加
一段打出来的「lazu」。字标的大小写与字重只有锁定件一种，页面上不得再出现 Geist
排的产品名当作标志。

## 5. Size, spacing and export

- Icon 最小数字尺寸：16 × 16px。
- Primary 最小显示高度：**28px**（字标 cap height ≈ 17px）；页头 32px、侧栏 30px、
  页脚 28px 用 Icon 与字标同一份 SVG 缩放，不得分别设置。
- 四周净空至少等于圆点直径（10/64 容器）。
- Primary 使用 `-15 0 W 80` 的紧凑画布，`W` 由字标的实际轮廓边界计算，字标左右各
  保留 15 单位净空。
- Wordmark 画布由轮廓边界加 15 单位净空得出，不含字体 advance 的空白。
- 常用栅格：16、32、48、64、128、150、180、192、256、512、1024px。
- SVG 是界面和文档的首选格式；PNG 用于 PWA、Apple touch、Windows tile、邮件和
  不支持 SVG 的下游；ICO 必须包含 16、32、48px。
- Apple Touch Icon 必须使用 180 × 180px 全幅不透明 Graphite 画布（方形，印章图形
  直接落在石墨面上），由 iOS 负责最终圆角蒙版。
- Open Graph 固定为 1200 × 630px，Stacked 居中。

## 6. Display typeface

字标的字体同时是产品的 display 字体：`--lazu-font-display`（Instrument Serif，
CJK 退回系统宋体）。它只用于公开页的 display 标题、章节标题、以及标题旁的大数字；
**任何低于 20px 的文字不用衬线**，界面正文、控件、表格继续 Geist，代码与 ID 继续
IBM Plex Mono。规则见 `public-web-system.md` 第 01 / 03 条。

## 7. Source and distribution

- 字体源文件与许可（OFL 1.1）：`brand/assets/fonts/`
- 字标轮廓：`brand/assets/logo/wordmark-outlines.json`，由
  `brand/assets/logo/extract-wordmark.py` 从字体生成（需要 `fonttools`）。
- 可编辑母版与兼容命名：`brand/assets/logo/current/`
- 全尺寸 PNG：`brand/assets/logo/raster/`
- 社交分享：`brand/assets/logo/social/`
- 自适应 favicon：`brand/assets/logo/favicon.svg`
- 可复现生成脚本：`brand/assets/logo/generate-assets.mjs`
- Web 组件资产：`apps/web/src/brand/assets/`
- Web 公共与 PWA 资产：`apps/web/public/`
- Web 自托管字体：`apps/web/src/styles/fonts/instrument-serif-*.woff2`
- Docs 资产：`apps/docs/public/`
- 对外下载包：`apps/lazu-logo-package/`

所有衍生资产必须由生成脚本统一更新，禁止在各应用目录中分别手工绘制。

生成前必须安装可执行文件 `rsvg-convert`（librsvg）与 `magick`（ImageMagick 7）。
先运行 `rsvg-convert --version` 和 `magick --version` 验证环境，再执行
`node brand/assets/logo/generate-assets.mjs`。生成器会在任何资产写入前预检这两个
依赖，缺失时直接退出，避免 SVG 与 PNG / ICO 版本不一致。字体或字距改动后先运行
`python3 brand/assets/logo/extract-wordmark.py`，再运行生成器。

## Seal continuation (2026-09-21)

The owner-approved Console exception is defined by
[yunmu-console.md](yunmu-console.md#locked-seal-refinement-2026-09-21):
page headings and display money use the display serif, while Models, dense
tables, controls and non-money figures retain their existing typography.
This explicit amendment takes precedence over the earlier public-only /
bold-money wording above.
