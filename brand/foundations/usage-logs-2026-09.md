# 用量与日志 — locked direction (2026-09-29)

Status: **LOCKED by user approval on 2026-09-29.** The user picked the list
redesign with **L1** (lane tag in the model cell), the inline detail **M3**
(C1 × C3: charge on the left, aligned time and cost bars on the right) and
**S1** (one official-price line under the charge) on the design canvas
`archive:brand/canvases.md`. The canvas keeps the
rejected directions (A side pane, B sheet, C1–C4, M1/M2, S2/S3). This file is
what `apps/web/src/pages/Log/` implements; where the canvas and this file
disagree, this file wins.

Route: `/console/usage`. It inherits [yunmu-console.md](yunmu-console.md),
[ui-system.md](ui-system.md) and the row geometry in
[console-review-2026-09.md](console-review-2026-09.md).

## List

1. **The lane is part of the model.** A request's lane rides in the model
   cell as a tag: `稳定` (ink dot) or `Agent` (ring dot, evidence colour). The two
   differ in lightness and dot shape, not hue alone. There is no 分组 column:
   the log's `group` _is_ the lane slug (`direct` / `cheap`).
2. **Only tag what is known.** A row whose group is not a lane (old tier
   groups, top-ups, system rows) and a failure refused before routing get no
   tag. `改走稳定` (warning tint, hint on hover) marks a request served on the
   stable lane instead of the lane its Key chose (`other.lane_requested`).
3. **Status is a dot and a word.** 成功 / 失败; failed rows are not painted.
   Other record kinds keep their type chip.
4. **Two merged columns.** `输入 → 输出` (18.4K → 1.2K, Latin K/M) and
   `用时 · 首字` (6.8s · 0.9s). No 详情 button column: the row opens the
   detail; the time cell is the keyboard handle and carries `aria-expanded`.
5. **Summary strip before the filters:** 花费 · 请求 · RPM · TPM, each a
   pending value until the query answers.
6. **Filters:** a `线路` switch (全部线路 / 稳定 / Agent) sets the lane; the
   customer's search box searches models. Operator columns (用户, 通道, 重试)
   appear only in the platform view.
7. **Amounts read to four decimals** (`$0.0213`); a request under a tenth of a
   cent keeps six so it never rounds up.

## Detail (M3)

The detail expands under its row on the same tint as the selected row; the
row above already names the model, lane and time, so the detail does not
repeat them (a deep link off the page is the exception and names them).

- **Left, 220px:** 实扣 (32px mono), then S1 — `官方价 ~~$x~~` and
  `省 $y（z%）` in success green. 官方价 prices input, output and cache at
  the lab's own list prices (models.dev); a request with a billed token line
  the catalogue has no list price for shows no comparison rather than one
  that counts that line at our price.
- **Middle:** two aligned bars. 时间: 首字 (neutral, at least 64px so its label
  never collides) then 生成 with tok/s and the total. 费用: one unlabelled
  segment per billed line, widths by amount, in the ledger's swatch colours.
- **Right, 380px — the F2 ledger** (canvas AhofMZpvkcdzABHogRGEMq, board F2).
  A header written once, `数量 × 单价 = 金额`; then one row per billed line
  with a swatch, the name and three right-aligned mono columns (62 / 72 /
  70px). Order: 输入, 图片输入, 缓存读取, 缓存写入 · 5 分钟, 缓存写入 · 1 小时,
  音频输入, 输出, 图片输出, 音频输出, per-call and tool lines, 最低计费.
  Per-call lines read `3 次` and `$10.00/千次` (count and price come from the
  request record; the line item keeps only the total); a line with only an
  amount leaves quantity and unit empty. With a discount or surcharge: a
  hairline, 小计, then `折扣 6 折 · −40% · −$x` (green) or `加价 20%` (amber);
  whole-order. Then an ink rule and 实扣 (14px, 600). Tags ride on the lines
  they apply to — 长上下文, and `×2` for peak hours (billing records
  `quote.peak.multiplier`) — with the reason in one line at the foot. On a
  phone each line becomes its name over `数量 × 单价 = 金额`.
- **Facts line:** 推理强度, 接口, IP, then the request id (the value of the
  `X-Request-Id` response header, searchable in the log) with 复制.
- **Failed requests** lead with 发生了什么 (the rejection's label and effect
  where known), whether anything was charged, then the upstream's own words.
- **Platform view only:** 通道, the retry chain and the upstream model.
  Customers do not see operator vocabulary.
- **No raw-data toggle, for anyone.** The log stores the billing record, not
  the request or the response, so its JSON only repeated what the receipt
  already shows. Debugging a request's content would need body capture — a
  separate decision about privacy, retention and disk, not a toggle on
  this page.

## Official price (S1) rules

1. **Snapshot at billing, never computed on display.** The gateway stores
   `quote.official.total_microusd` in the log's `line_items` when it bills.
   Rows billed before 2026-09-29 have none and show no saving.
2. **Only input and output have list prices** (`models.official_*_price`).
   Every other line counts at what was charged, so the saving can only be
   understated.
3. **No comparison** for a long-context tier, a minimum charge, a per-call
   model, or an input/output line whose list price is missing.
4. **Say nothing when there is no saving.** Under 1% (stable lanes usually)
   shows no line: never `省 0%`, never a negative number.
5. The saving is never repeated on list rows; a row-level badge would be on
   half the rows and read as noise.
