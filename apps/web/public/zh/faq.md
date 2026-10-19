<!-- Lazu · Lazu 常见问题：国内使用 Claude / GPT API、支付宝充值、价格与线路 · https://lazu.ai/zh/faq -->

# Lazu 常见问题：国内使用 Claude / GPT API、支付宝充值、价格与线路

## 可以自己部署 Lazu 吗?

Lazu 目前以 lazu.ai 托管服务的形式提供。如果你需要独立部署或私有化部署,联系我们,我们会和你过一遍可行的方案。

## 支持多少个模型？

所有可调用的模型都列在 lazu.ai/models，附实时价格和最近 7 天的可用率，来自 OpenAI、Anthropic、Google、DeepSeek、月之暗面（Kimi）、智谱（GLM）、阿里（通义千问）和 MiniMax。用 Key 调用 GET /api/models/catalog 可以拿到这把 Key 能用的同一份列表。

## 支持哪些模型厂商？

OpenAI、Anthropic、Google、DeepSeek、月之暗面（Kimi）、智谱（GLM）、阿里（通义千问）和 MiniMax。新模型上线后会出现在模型目录和更新日志里。

## 如何从 OpenAI 迁移过来？

改两行代码：把 base_url 从 https://api.openai.com/v1 改成 https://api.lazu.ai/v1，再把 api_key 换成 Lazu key。业务代码可以继续使用 OpenAI 官方 Python、JavaScript 或 Go SDK。

## 在中国能直接用 Claude 和 GPT 吗？

可以。使用 Lazu 的接口地址 https://api.lazu.ai，国内 IP 可直连，无需 VPN 或代理。每个模型页都有最近 7 天的可用率。

## 价格怎么算？

输入、输出和缓存 token 分别计量，按你所用线路的公开价格计费。无月费、无最低消费。

## 稳定线路和 Agent 专线有什么区别？

稳定线路走各家官方 API，按标价计费，可用率最高，适合所有场景。Agent 专线由第三方订阅池提供：通过官方客户端接入，不是官方 API。价格低很多，但可用率会有波动，失败时不会自动切到稳定线路，订阅池还会附带自己的预设上下文，所以只建议在对应的 Agent 工具里使用：Claude 模型配 Claude Code，GPT 模型配 Codex，Gemini 模型配 Antigravity。自己写的程序或需要精确控制输出的场景，请用稳定线路。两条线路用同一个 Key，可以在每把 Key 上按模型选线路。

## 支持什么支付方式？

通过 Stripe 在线充值，支持支付宝和 Visa、Mastercard 等银行卡。余额以美元计价。

## 会用我的数据训练模型吗？数据怎么保存？

不会，Lazu 不用你的数据训练模型。请求和响应的内容会记录在请求日志中，保留 180 天，方便我们与你一起回查具体请求，到期后删除。用量记录只保存元数据（时间、模型、token 数、费用、状态和 request_id），保留 90 天。Lazu 不会用你的内容训练模型，也不会出售。

## 支持流式响应吗？

支持。Chat completion 类 endpoint 支持 SSE 流式输出，传 stream: true 即可，行为与 OpenAI 兼容。

## 支持 Anthropic 原生 API 吗？

支持。除了 OpenAI 兼容的 /v1/chat/completions，Lazu 还提供 /v1/messages，用 x-api-key 和 anthropic-version header 发起 Anthropic 兼容请求。Extended Thinking、Prompt Caching 和 Tool Use 都走这条路径。

## 速率限制是多少？

速率限制取决于模型和你的账户。可以在控制台查看实时用量和限制；如需更高的 RPM 或 TPM，请联系 support@lazu.ai。

## 如何监控我的使用量和成本？

控制台提供按日、周、月汇总的使用仪表盘，并可按模型、endpoint 和 token 类型拆解。每次请求都会返回 x-request-id，可在日志页检索。

## 出错了怎么排查？

每个响应都会带 x-request-id 头。在控制台的「用量与日志」里搜这个 ID，可以看到请求时间、走的线路、状态、token 计费明细和错误详情。

## 有新用户优惠吗？

第一次充值多送 10%，最多送 $10，和这笔充值一起到账。注册本身不送额度，充值后不退款。

## 有推荐奖励吗？

有。在钱包里复制你的推荐链接（lazu.ai/?ref=你的名字），也可以在任何 lazu.ai 链接后面加 ?ref=你的名字。好友用它注册后 30 天内，每笔实付充值的 10% 归你，每位好友最多 $30，每笔 7 天后到账。每人最多算 100 位好友，同一个团队里的人不计入。奖励是 Lazu 额度，不能提现。
