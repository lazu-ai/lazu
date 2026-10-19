# Lazu Seal Logo Assets

正式版本为 `C · 印章 / Seal` + 字标 `W1 · Instrument Serif`（2026-09-20）。使用规则见 `brand/foundations/logo-system.md`。

- `wordmark-outlines.json`：字标「Lazu」的冻结轮廓，由 `extract-wordmark.py` 从 `brand/assets/fonts/InstrumentSerif-Regular.ttf` 生成。

- `current/`：Graphite、Paper、Primary、Stacked、Wordmark、Icon 与单色母版。
- `raster/`：16–1024px 的 Graphite / Paper PNG，以及 1280 宽（等比）Primary PNG。
- `social/`：1200 × 630 Open Graph SVG / PNG。
- `favicon.svg`：根据浏览器色彩偏好自适应的 favicon。
- `current/lazu-apple-touch-icon.svg`：由 iOS 施加圆角蒙版的全幅不透明 Apple Touch 母版。
- `generate-assets.mjs`：生成并分发 Web、Docs、下载包、PWA、ICO 和社交资产。

重新生成（字体或字距改动时先跑第一步）：

```bash
python3 brand/assets/logo/extract-wordmark.py
node brand/assets/logo/generate-assets.mjs
```
