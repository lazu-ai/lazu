# Request Lifecycle Contract

更新：2026-09-05（保留最初请求事实设计的兼容边界）

这份文档定义第一阶段先落地的请求事实 contract。它不是完整 data-plane kernel；当前目标是先把 usage、cache、request detail 和后续 billing/routing 的共同事实源稳定下来。

## 兼容范围（最初阶段的边界）

这份 contract 覆盖 #117 里和请求事实直接相关的部分：

1. 新请求在 consume log 里 dual-write `usage_fact`。
2. 新写入路径按 provider family 映射 UsageFact，不能继续靠模型名字符串决定 cache 语义。
3. `/api/usage/requests/{request_id}` 优先读取 `usage_fact`。
4. 老日志继续从 `log.Other` / routing outcome 降级读取。
5. 新 control-plane 出口使用真实 HTTP status + stable `error` object。

以下是最初阶段的排除项，不是对后续实现的禁止。当前结算与恢复规则见
[Billing Contract](billing-contract.md)。

最初阶段不做：

- 不拆 outbox / worker projection。
- 不改完整 BillingSettlement 状态机。
- 不删除 `log.Other`。
- 不全量重写 router middleware。

## UsageFact

`UsageFact` 是一次请求的标准用量事实，不是最终账单。后续 `PriceQuote` 根据它报价，`BillingSettlement` 根据 quote 扣款，request detail 根据它解释客户实际用了什么。

当前字段：

```json
{
  "version": 1,
  "provider_family": "deepseek",
  "prompt_tokens": 100,
  "completion_tokens": 30,
  "total_tokens": 130,
  "input_tokens": 60,
  "output_tokens": 30,
  "dimensions": [
    { "name": "input", "quantity": 60, "unit": "token" },
    { "name": "cache_read", "quantity": 40, "unit": "token" },
    { "name": "cache_miss", "quantity": 60, "unit": "token" }
  ],
  "provider_raw_usage": {
    "prompt_cache_hit_tokens": 40,
    "prompt_cache_miss_tokens": 60
  }
}
```

`dimensions` 使用现有 normalized usage dimension 名称：

- `input`
- `output`
- `cache_read`
- `cache_write`
- `cache_write_5m`
- `cache_write_1h`
- `cache_miss`
- `audio_input`
- `audio_output`
- `image_input`
- `web_search`
- `file_search`
- `image_generation_call`

## 用量来源与诊断

新文本请求的 `usage_fact.measurement` 和请求详情 `usage.measurement` 分别记录 `input` / `output` 的 `provider`、`local_estimate` 或 `unavailable`；它们不改变标准供应商 usage 的 wire schema。该字段可缺省，以兼容旧事实和未迁移的媒体事实。未知来源不能根据数值大小推断；`source` 仍表示请求来源（例如 Studio），不能借用来记录 token 来源。

`protocol_admission_prompt_tokens`、`protocol_local_prompt_tokens` 和 `protocol_admission_token_delta` 使用 `protocol_token_comparison: admission_vs_local_v1`，仅比较准入估值和本地估算。它们不是两套等价 tokenizer 的准确率对照。计数关闭或跳过媒体时不输出假零；历史 shadow 字段保留在旧日志中，不重写。

Responses 的 `previous_response_id`、`conversation` 和 `prompt` 状态参与请求 hash，避免把不同上下文混为重复请求；这些不透明状态不会进入文本 token 估算。

## Provider Mapping

usage mapper 按 provider family / upstream usage shape 处理，不按 adapter 数量处理。新写入路径必须显式带上 `provider_family`，并写进 `usage_fact.provider_family`；模型名猜测只能作为 provider-family 识别的兼容线索，不能决定 Anthropic/Claude cache input 是否需要和 cache read/write 分开计算。

原因是 Qwen、DeepSeek、OpenRouter 等模型常走 OpenAI-compatible adapter，但 raw usage 字段不同。第一阶段至少覆盖：

- OpenAI-compatible standard `prompt_tokens_details.cached_tokens`
- DeepSeek-compatible `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`
- Anthropic/Claude cache read/write buckets
- Gemini cached content and modality token details
- xAI/OpenAI-compatible fallback

新 UsageFact mapper 的规则：

- OpenAI-compatible / Qwen-compatible / DeepSeek-compatible / xAI：`prompt_tokens` 通常包含 cache read/write，标准 `input` 维度需要扣除 cache/audio/image 后得到。
- Anthropic/Claude：`input_tokens` 与 cache read/write 是 provider-separated 语义，标准 `input` 维度不能再扣一次 cache read/write。
- Gemini：从 `usage_metadata` 映射到标准 DTO 后，保留 cached content、text/audio/image modality dimensions。
- Realtime / streaming：使用最终 streaming usage 生成 UsageFact，不因为响应是 stream 就回到 `log.Other` 猜字段。

## Read Path

请求详情 API 的读取顺序：

1. 如果 `logs.usage_fact` 存在且能解析，使用它作为权威 usage。
2. 如果没有 `usage_fact`，按旧逻辑从 `log.Other` 和 routing outcome 降级读取。
3. 降级读取只为历史数据兼容，不作为新实现继续扩展。

## Rollback

如果新读取路径出问题，可以只关闭或回退 request detail 的 `usage_fact` 优先读取逻辑。因为第一阶段仍保留 `log.Other` 双写，旧 request detail 和旧计费路径可以继续工作。

如果 provider-family mapper 出现问题，可以回滚到旧的 log fallback；但 fallback 只用于历史兼容，不继续作为新 provider 的扩展点。
