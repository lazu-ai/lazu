# Lazu 协议转换设计基线

状态：Accepted

阅读顺序：先读稳定边界、流程与包职责；实现前问题移至文末历史背景。
后续计费与请求事实分别以 [Billing Contract](billing-contract.md) 和
[Request Lifecycle Contract](request-lifecycle-contract.md) 为准。

日期：2026-09-01

范围：生成式文本协议的互转内核。首个交付覆盖 OpenAI Chat Completions ↔ OpenAI Responses 双向（含流式）；IR 与能力模型按四协议（Chat、Responses、Anthropic Messages、Gemini Generate Content）预留语义空间，在第一批真实 fixtures 验证后冻结为 v1。

## 摘要

协议互转是网关基础能力，不是渠道配置项。本设计的目标是让系统只有少数稳定扩展点：

- 新协议 = 增加一个 Codec；
- 新语义 = 在中心 Feature/参数表增加一项；
- 新 provider = 增加 transport 方言（认证、URL、header、错误、usage）；
- 新模型 = 绑定 model family profile + 价格，不写代码；
- 真实例外 = 具名、可测试的 quirk。

为此采用：分族语义 IR（textgen 一族）+ 版本化 fixture 的协议 Codec + 带约束与 revision 的 typed 能力快照 + 每次尝试重新生成的不可变 RelayPlan + 响应事件 IR 与有状态 emitter。

本设计有意不包含：planner 数值成本函数、identity raw-body 快速路径、协议版本号机制、能力表的 Evidence/过期复核机制、多层 profile 继承、lossy 显式放行策略、状态模拟。理由与恢复条件见「明确不做」。

## 三条不变边界

实现中任何局部决定都不得违反：

1. **原始请求不可变，计划按 attempt 生成。** 解析出的语义 IR 在请求生命周期内只读；每个重试渠道从原始 IR 重新规划、重新编译 outbound，禁止复用或二次解析上一次尝试的产物。attempt 级数据放 `RelayAttempt`，不污染 `RelayInfo.Request`。
2. **能力与模型名解耦，catalog 与 data plane 共用同一份快照。** 禁止新的模型名前缀判断（`o*`、`gpt-5*` 类）；`/api/models/catalog` 的 endpoint/参数能力从同一快照投影。
3. **不能保真就明确拒绝，不静默转换。** 首个交付只注册无损转换边；覆盖不到的 feature 在调用上游前返回稳定 400（含 feature ID 和字段路径）。没有"悄悄丢参数"模式。

## 总体流程与路由接入点

```text
client wire request
  → pre-routing parse middleware：协议 Codec 解析 → textgen 请求 IR（不可变，入 context）
                                  + Feature 提取（一次遍历 → RequestFeatureSet）
  → Distribute：候选渠道能力预筛（只读 bitset/约束索引）→ 健康度/优先级选 attempt 渠道
  → 每 attempt：PlanAttempt → RelayPlan（不可变）
  → 上游 Codec 编译 outbound payload
  → provider transport（现有 adaptor 的 URL/认证/HTTP 部分）
  → 上游响应/流 → 解码为响应事件 IR
  → 客户端 emitter → client wire response/SSE
  → UsageFact → 计费（与客户端协议无关）
```

**路由接入点**：现状是 `Distribute()` 中间件先选渠道、controller 才解析请求，能力预筛无处安放。解法采用轻量 pre-routing parse：在 Distribute 之前完成一次协议 DTO/IR 解码并把不可变 IR + feature set 放入 context，Distribute 据此过滤渠道，controller 复用同一 IR，不二次解码。不采用「controller 接管选路」的大改。

middleware 只挂到本次覆盖的 textgen 路由，不能挂到同时包含 image/audio/embedding 的共享 group。固定顺序为：

```text
Responses: body limit → rate limit → file dereference → textgen parse → distribute
Chat:      body limit → rate limit → textgen parse → distribute
```

`DereferenceFileIDs` 可能先做一次 JSON traversal，因此性能合同是「协议 DTO/IR 只解码一次」，不是字面上的「body 只被 JSON 读取一次」。Claude/Gemini 在尚未迁移 codec 时保持旧路由顺序。

预筛必须发生在随机/加权选中单个渠道**之前**，否则可能抽中不兼容渠道而失败，即使候选集合里存在可用渠道。重试只会换一次渠道（廉价档一次都不换），所以不能指望重试把这种失误兜回来。

路径偏好是固定顺序，不是成本函数：

```text
native（同协议直达） > lossless bridge（一次语义转换）
其余（lossy / unknown / unsupported） = 拒绝
```

自动路径最多一个语义 hop。

## 包边界

不新建平行宇宙，在现有结构上收敛：

```text
internal/protocol/textgen/
  ir.go            # 请求 IR（typed，presence 语义）
  event.go         # 响应事件 IR
  features.go      # FeatureID 常量 + 从 IR 提取 FeatureSet
  params.go        # 中心参数语义表
  plan.go          # RelayPlan + PlanAttempt()
  codec/openai_chat/        # wire ↔ IR，含 SSE 解码/emitter
  codec/openai_responses/   # wire ↔ IR，含 SSE 解码/emitter

packages/domain/providercap/   # 从 bool Profile 演进为能力快照（见能力模型）
```

### 现有代码的归宿与复用清单

**作为新 Codec 起点与 golden baseline 复用，禁止从零重写**：

- `ChatCompletionsToResponsesRequest` 的消息、工具、JSON Schema 映射（[openai_responses_compat.go](../internal/service/openai_responses_compat.go)）；
- `OpenAIResponsesToChatCompletionsResponse` 的文本、tool call、annotations、usage 映射；
- [relay_responses.go](../internal/relay/channel/openai/relay_responses.go) 的流式兼容处理经验与 fixture；
- `ValidateOutboundResponsesStore` 的原生 Responses preflight；
- provider transport（URL、认证、header、HTTP client）、raw usage → UsageFact 映射；
- 现有计费、预扣、退款合同；unknown-field / disabled-field 的安全处理测试。

**迁移后删除**（在真实 E2E 与计费验证通过后，同一交付序列内）：

- `ResponsesFirstForChatEnabled`、`ResponsesCompatForChat`；
- `TextHelper` 中对 `RelayMode`/`RequestURLPath` 的临时修改及 response handler 识别兼容标志的分支；
- `shouldUseResponsesFirstForChat` 的模型/参数判断。

provider adaptor（[adapter.go](../internal/relay/channel/adapter.go)）长期只保留 transport 职责；Claude/Gemini adaptor 与 embeddings/图像/音频/Realtime 在本次交付中**不动**。

## 请求 IR

IR 表达语义，Codec 负责字段形状。typed、可演进，禁止 `map[string]any` 承载核心语义。

```go
type Request struct {
    Model        ModelRef
    Conversation []InputItem       // tagged union，保持顺序
    Instructions []Instruction     // system/developer
    Tools        []ToolDefinition  // function 与 hosted 分开建模
    ToolPolicy   ToolPolicy        // tool_choice、parallel
    Output       OutputConstraint  // json_object / json_schema / strict
    Sampling     SamplingOptions   // 全部 Option[T]，见 presence
    Reasoning    ReasoningOptions  // effort、summary
    Lifecycle    LifecycleOptions  // stream、stream usage、store、include
    Metadata     MetadataOptions   // metadata、user、safety_identifier
    Raw          RawRetention      // 仅同协议 round-trip 用，见未知字段
}
```

### Presence 语义

现有 DTO 用非指针标量 + `omitempty`，`temperature: 0`、`top_p: 0`、`store: false` 的显式提供会丢失（如 `dto.OpenAIResponsesRequest.Temperature float64`）。IR 中所有可选标量一律 `Option[T]`（或指针），区分「未提供」与「显式零值」。Codec 的 fixture 必须覆盖两者。

### InputItem

tagged union：message（含 text/image/audio/file content part）、function call、function result、reasoning item、reference。必须保持 item 顺序、role、call ID、内容类型；工具调用与结果不得折叠成字符串消息。provider opaque 内容（reasoning signature 等）随 item 保留，但只允许回传给来源相同的 provider dialect。

### 对 Claude / Gemini 的纸面试穿（冻结 IR v1 前的验证）

Claude/Gemini codec 不在本次交付内，但 IR v1 冻结前必须用现有真实 payload/fixture 验证装得下，避免 IR 退化成"换皮 OpenAI"。下表是最低覆盖清单，不以纸面映射替代 fixture：

| 协议概念 | IR 落点 |
| --- | --- |
| Claude `system`（string 或 block 数组） | `Instructions`（保持多段） |
| Claude `thinking` block + signature | `InputItem{Reasoning}`，signature 存 opaque 字段并绑定来源 dialect |
| Claude `tool_use`/`tool_result`（自有 ID 格式） | function call/result item；call ID 原样保留，不做格式归一 |
| Claude `tool_choice: {type: any}` | `ToolPolicy` 的 required 语义 |
| Gemini `systemInstruction` | `Instructions` |
| Gemini `functionCall`/`functionResponse`（无 ID，按顺序配对） | function call/result item；ID 为空时以顺序配对语义表达，Codec 侧生成/剥除合成 ID |
| Gemini `generationConfig.responseSchema` | `OutputConstraint` |
| Gemini `thinkingConfig` | `ReasoningOptions` |

实现 Chat/Responses codec 时如发现 IR 需要为上表让路的改动，先改 IR 再继续，不留"以后再兼容"。

## 中心参数语义表

普通标量参数只声明一次，不按模型适配：

```go
type ParameterSpec struct {
    ID              string     // "temperature"
    RequiredFeature FeatureID  // FeatureSamplingTemperature
    Conflicts       []string   // 互斥组
    Validate        func(v Value) error
}
```

新增一个普通采样参数的完整流程：① 在 IR 与参数表声明；② 在真正拥有该字段的协议 codec 里加读写映射；③ 各 model family 通过能力快照继承支持。没有第四步。

复杂参数（structured output、tools、reasoning、multimodal）有跨字段不变量，走 typed codec 逻辑，不进通用 rename 机制。

### 渠道 ParamOverride 分为两层

现状是 override 在转换后直接打在 raw JSON 上（`ApplyParamOverride`），可能绕过能力校验、注入 plan 未批准的 feature。目标分层：

1. **typed semantic override**（temperature、max tokens 等语义参数）：在候选渠道规划阶段应用到 IR 副本，随后**重新提取 effective feature set** 再进 plan；
2. **wire dialect patch**：outbound 编译后应用，只允许 provider 字段形状/URL 方言修补，应用后重跑 outbound 校验；不得改 model、新增 feature、改变工具执行责任。已落地为 `internal/relay/wirepatch`（注册表 + 每 handler 一个挂点，位于 ParamOverride 之后）；每个 quirk 是一条带 ID、方言、原因、来源链接和回归测试的注册项，细节见 [wire-dialect-patch.md](./wire-dialect-patch.md)，规则见 AGENTS.md Protocol Layer 第 6 条。

迁移前先盘点线上渠道 `param_override` 的真实使用情况，把存量配置归类进两层，避免破坏管理页面已有配置；盘点结果决定兼容期的解释规则。

## 能力模型

### 形状

```go
type CapabilityKey struct {
    Dialect     string  // openai / azure_openai / cloudflare_ai_v1 / anthropic / gemini ...
    ModelFamily string  // kimi-k2 / gpt-5 / claude-4 ...
}

type CapabilitySnapshot struct {
    Revision    string                      // 热更新原子换、审计、plan 记录、缓存 key
    Features    FeatureSet                  // bitset：有/无
    Constraints map[FeatureID]Constraint    // 数值/枚举约束：context window、max output、
                                            // schema 大小与嵌套、tool 数量、参数取值范围
}
```

### Capability identity、合成与迁移

能力的身份来源必须显式持久化，不能在热路径继续猜 base URL 或模型名前缀：

- `Dialect` 来自渠道的 `protocol_dialect` 设置。专用 provider 渠道由页面模板自动写入；Cloudflare Workers AI 使用 `cloudflare_ai_v1`；普通 OpenAI-compatible 渠道默认使用保守的 `openai_chat_compatible`。
- `ModelFamily` 来自模型元数据/价格配置中的显式 family。现有模型名推断只允许用于一次性 backfill 和人工复核，不能作为新 data-plane 决策逻辑。
- 现有 `ChannelOtherSettings.Capabilities` 收敛为 channel capability overlay，不另建平行的渠道能力系统。
- endpoint overlay 使用可选的 `supports_chat_completions` / `supports_responses`；仅显式 `false` 收窄 dialect 基线，缺省不改变老渠道行为，也不作为请求级协议开关。
- 老渠道迁移时按已知 channel type/provider 模板 backfill dialect；不能可靠识别的渠道落到保守 profile，不乐观声明 Responses 等 endpoint。

生效能力固定为：

```text
dialect baseline
  ∩ model-family profile
  ∩ channel capability overlay
  − operational disables
```

合并规则固定为：数值上限取最小值，枚举能力取交集，显式禁用优先；未声明按不支持。channel overlay 和运维配置默认只能收窄能力，不能凭空扩大；扩大能力必须修改 dialect/model profile 并通过 contract tests。合成后的整张快照产生新的 `Revision`。

- **endpoint 可用性建模为 feature**（`feature.EndpointChatCompletions`、`feature.EndpointResponses` 表示原生存在），key 保持两级，不为 endpoint 增加第三维；同一方言两个 endpoint 的 feature 差异记为 `EndpointGaps`（按方言声明，如 DeepSeek Chat 不收 json_schema），planner 据此把请求路由到另一端点，见 [protocol-degradation.md](./protocol-degradation.md)；bridge 可用性由 planner 派生（原生 endpoint 缺失 + 存在无损边 + 请求 feature 全覆盖），不进快照。这直接修正 Cloudflare 误判：`cloudflare_ai_v1` dialect 不声明 `EndpointResponses`，planner 自然选 bridge 路径。
- dialect 决定 wire 层能力（endpoint、stream framing、usage 方言）；model family 决定语义能力与数值约束；单模型只在真实差异时 override 并写明依据；
- 运维临时禁用是 (key, feature) 黑名单，与快照一起原子热换，换后 `Revision` 变化；
- 没声明就是不支持（无 `Unknown` 态）。表的准确性靠"条目少而粗 + codec contract tests"维持；`Evidence`/过期复核机制暂缓（见明确不做），但 `Revision` 必须保留——否则热更、回滚、日志审计和 planner 缓存失效都无从谈起。

现有 [providercap/profile.go](../packages/domain/providercap/profile.go) 的四个 `Supports*` bool 演进为此快照；`IsSupported(apiType, relayFormat)` 的调用方迁移到 `Plan` 检查，签名可保留作过渡壳。

### Feature 清单（首个交付需要的最小集）

- endpoint：chat_completions、responses（原生存在性）；
- 输入：text、image、file、developer role；
- 工具：function、parallel、tool result、web search（hosted）；
- 输出：json_object、json_schema、strict；function tool 的 `strict` 也作为 tool 定义语义保留；
- 推理：effort、summary；
- 采样：temperature、top_p、seed、stop、n、logprobs、max output；
- 生命周期：stream、stream usage；
- `include` 按取值拆分（见拒绝语义），不作为单一 feature；
- 状态类（只用于拒绝）：previous_response、conversation、prompt template。

### Catalog 投影与聚合语义

`/api/models/catalog` 从同一快照投影，多渠道模型的聚合规则：

- token 可访问的渠道中，只要**至少一个**渠道能无损（native 或 lossless bridge）完成该 endpoint，就列为支持；
- planner 按本次请求 feature 选择真正兼容的渠道，catalog 声明与 plan 决策必须一致；
- transient 健康波动不改变"协议支持"；但该 endpoint 的全部兼容渠道被禁用后，应从 token-scoped catalog 消失；
- 管理端额外展示 native/bridged 区分，作为诊断元数据；
- 同一模型无论客户端走哪个协议，计费由同一上游 usage 与价格合同决定。

## RelayPlan

```go
type RelayPlan struct {
    ClientProtocol   Protocol   // {Family, Dialect}，无 Version 字段
    UpstreamProtocol Protocol
    RequestCodec     string
    ResponseEmitter  string
    Native           bool       // false = lossless bridge
    CapabilityRev    string     // 本次决策使用的能力快照版本
    Features         FeatureSet // 本次请求实际使用的
    Warnings         []Issue    // 如 stateless bridge 标记
    ChannelID        int
    UpstreamModel    string
}
```

一经创建不可修改。规划顺序：解析 canonical model → 提取 feature set →（typed semantic override 后重提取）→ 候选渠道能力预筛 → 健康度/优先级选 attempt 渠道 → 生成 plan → 编译 outbound → ParamOverride → wire dialect patch（[`internal/relay/wirepatch`](./wire-dialect-patch.md)）→ outbound 校验。

## 响应与流式

### 事件 IR

请求 IR 与响应 IR 不复用。响应是**内容事件**序列（`internal/protocol/textgen/event.go`，2026-10-21 修订）：它只说"模型说了什么、按什么顺序"，不带任何一个协议的生命周期。Responses 的 item/content part/sequence_number、Anthropic 的 block、Chat 的 choice 都是各协议 emitter 自己的状态。

```go
type Event struct {
    Kind       EventKind   // start, turn, text, refusal, reasoning_start, reasoning,
                           // reasoning_signature, reasoning_sealed, tool_start,
                           // tool_arguments, tool_done, stop, usage, error
    Text       string      // 分片：回答、拒绝、推理、签名或参数
    ResponseID, Model string; CreatedAt int64   // start
    ItemID     string      // 上游给推理块或调用 item 的 id
    ToolIndex  int; ToolCallID, ToolName string // 调用按上游位置合并
    Stop       StopReason  // end / length / tool / filter
    Usage      *dto.Usage  // 共享用量事实
    Error      *SemanticError
    Sealed     json.RawMessage; SealedBy string // 密封推理，只回传给来源方言
}
```

每个协议有一个 **decoder**（上游 wire → 事件）和一个 **emitter**（事件 → 客户端 wire）；4 个协议 8 件，16 个组合全部由此得到。emitter 是显式状态机，持有 O(活跃 item/tool 数) 状态，自己分配 id、index 和 sequence。Responses emitter 的 terminal event 必须带完整 `output`，所以它保留一份受请求 max-output 约束的装配缓冲；禁止重复缓冲、无界队列或为日志再复制一份完整输出。推理块边界（`reasoning_start`）和密封推理（`reasoning_sealed`）在 IR 里表达，这样 Anthropic 的多段 thinking 和 Responses 的 reasoning item 不需要 emitter 之间互相认识对方的形状。

已落地（流层）：Chat decoder 与 emitter（`codec/openai_chat/stream.go`、`emitter.go`）、Responses decoder 与 emitter（`codec/openai_responses/decoder.go`、`stream.go`）、Anthropic decoder 与 emitter（`codec/anthropic_messages/stream.go`、`emitter.go`），以及把事件装配成整段回复的 `textgen.Collector`（非流式渲染和 emitter 的终态都从它读）。三个协议的 decoder 都有录制 fixture 驱动的测试，跨协议组合（Anthropic→Chat、Anthropic→Responses、Chat→Anthropic、Responses→Chat、Responses→Responses）各有一条端到端测试。尚未接入 handler：请求侧 codec（IR↔Claude 请求）、规划器的 Anthropic 边和派发统一是下一步；接入后 `relay_responses.go` 的直接转换、`relay-claude.go` 和 `service/convert.go` 的逐对转换器删除。

### 流式不变量（fixture 必须逐条覆盖）

- sequence 单调递增；response/item/content/tool call ID 全流稳定；
- added 先于首个 delta，done 晚于末个 delta；content_part added/done 成对；
- function arguments 分片不与文本 delta 混流；
- terminal event（completed/incomplete/failed）恰好一次，incomplete 必须带原因；
- 末 chunk 单独出现的 usage 必须并入同一 UsageFact；
- 客户端取消立刻取消上游 context；
- 已向客户端发出响应字节后禁止切换渠道重试；
- bounded buffer + 背压，慢客户端不得无限占用内存。

### 流中错误

header 未发出：普通 HTTP 错误。header 已发出：按客户端协议发合法 terminal error event、结束流、不重试，按是否已有可信 upstream usage 结算或退款。

## 拒绝语义与边界情况

### 转换错误不是渠道错误

本地无法转换 → 调用上游前返回 400。不消耗重试、不触发 auto-ban、不计入上游 5xx 与健康度、不包装成 502。上游 400 也**不是**"换个协议再试"的信号。

错误格式：

```json
{
  "error": {
    "type": "unsupported_feature",
    "code": "protocol_bridge_unsupported",
    "param": "previous_response_id",
    "message": "The selected route uses a stateless chat-completions upstream and cannot preserve previous_response_id.",
    "request_id": "req_..."
  }
}
```

### `store`：三态处理

Responses API 的 `store` 默认为 true；bridge 路径按取值区分，而不是一刀切：

| 请求取值 | bridge 路径行为 |
| --- | --- |
| `store: false` | 正常接受（bridge 本就无状态） |
| 未提供（SDK 默认） | 接受，request detail 标记 `stateless_bridge`，保证官方 SDK 默认请求可用 |
| 显式 `store: true` | 400 `unsupported_feature`——用户明确要求持久化而 bridge 无法提供；现在拒绝好过"稍后按 response id 取回"时静默失败 |

理由：一刀切 400 会打死所有 SDK 默认请求；一刀切 no-op 则改变了 OpenAI 字段语义、把显式意图静默吞掉。三态同时满足兼容性与「不能保真就明确拒绝」边界。native 路径 `store` 原样透传。

**Warning 载体**：`X-Lazu-Warning` 响应头 + request detail；不向标准 OpenAI response body 新增字段。实现时同步 OpenAPI、public API docs、Lazu API skill 和 CORS exposed headers，确保浏览器客户端可读取该 header。

### `include`：按取值拆 feature

`include` 不作为整体拒绝。各取值是不同 feature：`reasoning.encrypted_content` 在 bridge 路径 400；其余取值逐个判定可映射或拒绝，进能力快照与 contract tests。

### 其余 stateful 与 hosted

- `previous_response_id`、`conversation`、prompt template：bridge 路径 400；
- hosted tools：只有上游原生支持才声明支持；不得把 hosted tool 改写成 function tool 假装等价；
- **web search 映射（conditional）**：Chat `web_search_options` ↔ Responses `web_search` tool 的既有映射保留在 OpenAI dialect 内部使用，但其输出 item、annotations、usage 行为差异未经验证，**在 contract tests 证明前不得标记 lossless**，不参与自动 bridge 决策；CF Kimi 等不支持 web search 的组合由能力快照直接 400；
- reasoning：只转换协议明确公开的内容（effort、summary、可见文本）；opaque item/signature 绑定来源 dialect，禁止跨 provider 转发；不承诺迁移隐藏思维链。

### 未知字段：跨协议一律 400

- 同协议：经 `Raw` 保留 round-trip（延续现有 `mergeUnknownResponsesFields` 行为，受 allowlist 与大小限制约束）；
- 跨协议：无法安全推断含义，400。代价是上游发布新字段后 bridge 路径需 Lazu 声明才可用（native 路径不受影响）——接受此代价，换取不静默改语义。

## Usage 与计费

- provider raw usage 是首选事实，映射进现有 `UsageFact` 后再投影为客户端协议的 usage 输出；协议转换不改变账单。
- token 预估从入口 DTO 的 `ParseInput` 简化路径迁到 IR 规范化结果（文件、图片、tool schema、多段 content 同一遍提供给限流与预扣费）。**迁移期双算对比**，确认预扣差异可解释后再切换。
- 缺 usage 时的本地估算标记 estimated，遵循现有失败/退款合同。

## 可观测性

每请求记录（不含密钥与默认完整 payload）：client/upstream protocol、native/bridge、capability revision、feature set、warnings、conversion error 分类、planning/compile/TTFB/流转换耗时、UsageFact 来源。

指标：`protocol_plan_total{native|bridge}`、`protocol_conversion_failures_total`、`protocol_unsupported_total{feature}`、`protocol_stream_event_duration`。转换失败不进渠道健康率。管理后台请求详情展示转换摘要（plan、warnings、拒绝原因、能力版本）。

## 性能

工程目标（benchmark 校准，非 SLA）：协议 DTO/IR 只解码一次（pre-routing parse 是唯一协议解码点）；bridge 编译 P95 < 2ms；SSE 单事件转换 P95 < 100µs；无按 payload 大小的多余完整拷贝；能力快照只读 + 原子指针热换。

不做 raw-body identity 快速路径：现有链路本就全量解析，先保正确性；若 benchmark 显示同协议路径成为瓶颈，再作为独立优化立项（届时必须过 disabled fields、model mapping、隐私策略的安全审查）。

## 测试策略

1. **Golden baseline 先行**：把现有转换函数与测试固化为版本化 fixture，任何迁移不得改变既有正确行为。
2. **Codec contract fixtures**（每协议、版本化）：wire→IR、IR→wire、事件流→IR→客户端流；显式零值 vs 未提供；未知字段；malformed union；边界尺寸。
3. **无损 round-trip 属性测试**：对声明无损的 feature 验证 `wire A → IR → wire B → IR` 语义等价；event 序列恒满足状态机不变量；未知字段不跨 provider 泄漏；converter 不 panic、不产生重复 ID。
4. **能力快照 contract tests**：每个 (dialect, model family) 断言生效 feature set 与约束，防止表失真。
5. **E2E**（CF Kimi K2.6 + 一个原生 OpenAI 渠道）：Chat/Responses × stream/non-stream × tools/structured output；usage、钱包、请求详情；重试到不同 dialect 从原始 IR 重规划；客户端取消；不支持 feature 的稳定 400；store 三态行为。

测试矩阵按 feature class 组织，不做模型×参数笛卡尔积。

## 交付方式

「一次做完」= **一个完整项目、一个 PR**。原先列出的 6 项不是 6 个 PR，而是同一 PR 内的检查点；旧路径不跨 PR 长期并存。为控制风险，该 PR 按三个内部 checkpoint 实施和验证，但只在全部验收通过后一次提交评审：

1. **合同与内核**：固化 golden fixtures；落地 textgen IR、feature/参数表、能力身份/快照/backfill；实现 Chat/Responses 非流式 codec。
2. **流式与编排集成**：完成双向 SSE 状态机；接入 pre-routing parse、能力预筛、catalog 聚合、每 attempt 重规划；迁移现有 Chat→Responses；接入 UsageFact、指标与 request detail。
3. **验证与清理**：完成真实 Chat/Responses × stream/non-stream E2E、钱包/计费/取消/retry 验证；确认无回归后删除 `ResponsesFirstForChatEnabled`、`ResponsesCompatForChat`、`TextHelper` 的 RelayMode/URL 临时修改及相关兼容分支。

回滚单位是整个 PR/commit，不依赖继续保留旧 feature flag；这避免两套协议决策同时存在，也符合本次只交付一个 PR 的约束。

### 验收标准

- CF Kimi K2.6 同时通过 `/v1/chat/completions` 与 `/v1/responses` 正常访问，无渠道级协议开关；渠道实际打到 `/v1/chat/completions`（Cloudflare 不再被误判为原生 Responses）；
- 官方 SDK 默认参数可用；`store` 三态行为符合决策；
- 显式零值、未知字段、tool call ID、stream sequence（含 content_part 与 incomplete 终态）不丢失不重复；
- `previous_response_id` 等不可保真功能在上游调用前返回稳定 400；
- 转换失败不触发 auto-ban、不进上游健康失败指标；
- 重试到不同 dialect 时从原始 IR 重新规划，无上一 attempt 的状态残留；
- Chat 与 Responses 对同一次上游 usage 产生一致账单；
- catalog 展示的 endpoint 能力与实际 plan 决策一致（含多渠道聚合语义）；
- 旧 flag 与兼容分支已删除。

## 决策记录

定稿时确定的关键决策，便于回溯（详细理由见各对应章节）：

| 事项 | 裁决 |
| --- | --- |
| `store` 在 bridge 路径 | 三态：false 接受；缺省接受 + request detail 标记；显式 true → 400 |
| Warning 载体 | `X-Lazu-Warning` 头 + request detail，不改标准 body |
| 跨协议未知字段 | 一律 400，接受"新字段需声明"的跟进成本 |
| Catalog 聚合 | ≥1 个无损渠道即列为支持；全禁用则从 token-scoped catalog 消失；管理端展示 native/bridged |
| 交付方式 | 完整项目 + 1 个 PR；3 个 checkpoint/原 6 项仅作内部验证清单；旧 flag 在该 PR 内删除 |
| 能力快照 | 显式 Dialect/ModelFamily + 现有 channel overlay；Features + Constraints + Revision；endpoint 建模为 feature |
| 事件 IR | 补全 ResponseID/ItemIndex/Status/content_part 生命周期/incomplete reason/metadata |
| ParamOverride | 拆 typed semantic override（规划前）与 wire dialect patch（编译后），迁移前盘点存量 |
| 路由接入点 | textgen 专属 pre-routing parse；Responses 在 file dereference 后、Distribute 前；协议 IR 只解码一次 |
| `include` | 按取值拆 feature，逐个判定 |
| web search 映射 | conditional，contract tests 证明前不标 lossless |
| 文档归宿 | 实现稳定后收敛为 ADR，保留 trade-off 历史，不删除 |

## 明确不做（及恢复条件）

| 项 | 状态 | 恢复条件 |
| --- | --- | --- |
| Claude/Gemini codec 迁移 | 推迟 | IR v1 先用其真实 fixture 验证后冻结；bridge 稳定后单独迁移 codec，届时收敛 adaptor 的 Convert* 接口 |
| lossy 显式放行（token/workspace policy） | 2026-10-20 部分放开 | 不做通用放行；只做有界降级注册表（固定规则、具名、带来源与测试、仅对渠道**已知**不支持的 feature 生效）加按端点的能力缺口路由，见 [protocol-degradation.md](./protocol-degradation.md) |
| 状态模拟（ResponseStateStore） | 不做 | 明确客户需求 + 独立的存储/加密/保留期/租户隔离设计，单独 ADR |
| hosted tool 网关运行时 | 不做 | 产品层面决定做 tool 执行环境时 |
| planner 数值成本函数 | 不做 | 出现 >2 条候选路径需要权衡时（预计不会） |
| identity raw-body 快速路径 | 不做 | benchmark 证明同协议路径是瓶颈 |
| Capability Evidence/过期复核 | 不做（Revision 保留） | 能力表出现第三方/自动化来源时 |
| ProtocolID.Version | 不做 | 某协议真的发布不兼容版本时，在 codec 层加 |
| 通用 JSON 转换 DSL | 不做 | 永不（typed codec 是长期答案） |

## 文档生命周期

本文档是实现基线。实现合入并稳定后，收敛为 ADR（保留决策与 trade-off 历史），操作性内容沉淀到 `docs/request-lifecycle-contract.md` 与代码注释；本工作稿届时移除。

## 历史背景：2026-09-01 实现前现状与根因

以下记录设计提出时的问题，不代表当前分支仍有这些缺陷。

实现前的双向覆盖矩阵：

| 客户端协议 | 上游协议 | 现状 |
| --- | --- | --- |
| Chat | Chat | 正常主路径 |
| Chat | Responses | 已实现（`ResponsesFirstForChatEnabled` flag），能力覆盖有限 |
| Responses | Responses | 原生透传 |
| Responses | Chat | **未实现**（Kimi/CF 需要的方向） |

三个已确认的结构性问题，本设计必须一并解决，不允许旁挂修补：

1. **Cloudflare 被误判为原生支持 Responses。** CF Workers AI 以 `ChannelTypeOpenAI` 接入，[providercap](../packages/domain/providercap/profile.go) 里 `APITypeOpenAI.SupportsResponses = true` 一刀切，导致 Chat-only 方言被送往不存在的 `/responses`。这是 Kimi `/v1/responses` 不通的根因——不是缺转换器，而是能力判断的粒度错了。禁止用 `if cloudflare` 特例修补。
2. **兼容路径污染共享可变状态。** [compatible_handler.go](../internal/relay/compatible_handler.go) 的 Responses-first 分支直接改 `info.RelayMode`、`info.RequestURLPath`、`info.ResponsesCompatForChat`；上游调用已发出后再遇到可重试错误时不会恢复，下一个 attempt 复用同一 `RelayInfo`，可能带着上一次的决策走错 handler。「每 attempt 不可变 RelayPlan」是在修这个现存正确性风险，不是架构洁癖。
3. **协议语义散落三层**：service 转换函数、adaptor `Convert*Request`、response handler 兼容分支 + `RelayInfo` 标志 + `shouldUseResponsesFirstForChat` 的模型/参数判断。只补 Responses→Chat 会制造第二套决策与第二套流式状态，必须收敛编排层。
