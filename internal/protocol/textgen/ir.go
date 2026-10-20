package textgen

import "encoding/json"

// Option preserves the difference between an omitted field and an explicit
// zero value. That distinction is part of the wire contract for both Chat
// Completions and Responses.
type Option[T any] struct {
	Value T
	Set   bool
}

func Some[T any](value T) Option[T] { return Option[T]{Value: value, Set: true} }

type ProtocolFamily string

const (
	ProtocolOpenAIChat      ProtocolFamily = "openai_chat"
	ProtocolOpenAIResponses ProtocolFamily = "openai_responses"
	ProtocolAnthropic       ProtocolFamily = "anthropic_messages"
	ProtocolGemini          ProtocolFamily = "gemini_generate"
)

type Protocol struct {
	Family  ProtocolFamily `json:"family"`
	Dialect string         `json:"dialect"`
}

type ModelRef struct {
	Canonical string
	Upstream  string
	Family    string
}

type Instruction struct {
	Role string
	Text string
	// CacheControl is Anthropic's prompt-caching marker on this instruction
	// (a Chat system part's cache_control). See VendorOptions.
	CacheControl json.RawMessage
}

type InputKind string

const (
	InputMessage        InputKind = "message"
	InputFunctionCall   InputKind = "function_call"
	InputFunctionResult InputKind = "function_result"
	InputReasoning      InputKind = "reasoning"
	InputReference      InputKind = "reference"
	// InputOpaque is a Responses input item whose wire type the codec does not
	// model (for example Codex's "additional_tools"). It is retained verbatim
	// for native relays and can never be bridged to another protocol.
	InputOpaque InputKind = "opaque"
)

type ContentKind string

const (
	ContentText  ContentKind = "text"
	ContentImage ContentKind = "image"
	ContentAudio ContentKind = "audio"
	ContentFile  ContentKind = "file"
)

type ContentPart struct {
	Kind     ContentKind
	Text     string
	URL      string
	Data     string
	Format   string
	FileID   string
	Filename string
	Detail   string
	// CacheControl is Anthropic's prompt-caching marker on this part. See
	// VendorOptions.
	CacheControl json.RawMessage
}

type OpaqueValue struct {
	Dialect string
	Value   json.RawMessage
}

type InputItem struct {
	Kind         InputKind
	Role         string
	Name         string
	Content      []ContentPart
	CallID       string
	FunctionName string
	Arguments    string
	Output       string
	Opaque       []OpaqueValue
	// PlainReasoning is present only for a recognized stateless gateway item.
	// Opaque retains the original wire value for native Responses forwarding.
	PlainReasoning Option[string]
	// WireType is the original item "type" for opaque items, kept so planner
	// diagnostics can name what the client actually sent.
	WireType string
}

type ToolDefinition struct {
	Type        string
	Name        string
	Description string
	Parameters  json.RawMessage
	Strict      Option[bool]
	Hosted      json.RawMessage
	// CacheControl is Anthropic's prompt-caching marker on this tool. See
	// VendorOptions.
	CacheControl json.RawMessage
}

// VendorOptions are fields of Anthropic's Messages API that a client sends
// through another protocol's wire, as OpenRouter-compatible Chat clients do:
// a request-level cache_control marker and the speed tier (the per-part,
// per-instruction and per-tool markers sit on those items). They are not
// unknown fields: the Messages encoder writes them, a bridge to any other
// protocol refuses them (AnthropicOptionPath), and a native relay passes its
// own wire untouched.
type VendorOptions struct {
	CacheControl json.RawMessage
	Speed        json.RawMessage
}

type ToolPolicy struct {
	Mode              string
	FunctionName      string
	ParallelToolCalls Option[bool]
}

type OutputConstraint struct {
	Type        string
	Name        string
	Description string
	Schema      json.RawMessage
	Strict      Option[bool]
}

type SamplingOptions struct {
	Temperature Option[float64]
	TopP        Option[float64]
	TopK        Option[int]
	Seed        Option[int64]
	Stop        Option[json.RawMessage]
	N           Option[int]
	LogProbs    Option[bool]
	TopLogProbs Option[int]
	MaxOutput   Option[uint]
}

type ReasoningOptions struct {
	Effort  string
	Summary string
	// Budget is a thinking budget in tokens (the OpenRouter-style
	// reasoning.max_tokens of a Chat request); Messages carries it as
	// thinking.budget_tokens.
	Budget Option[int]
}

type LifecycleOptions struct {
	Stream             bool
	StreamIncludeUsage Option[bool]
	Store              Option[bool]
	Include            []string
	PreviousResponseID string
	Conversation       json.RawMessage
	PromptTemplate     json.RawMessage
}

type MetadataOptions struct {
	Metadata         json.RawMessage
	User             string
	SafetyIdentifier string
	ServiceTier      string
	PromptCacheKey   json.RawMessage
}

type RawRetention struct {
	Protocol      ProtocolFamily
	TopLevel      map[string]json.RawMessage
	UnknownFields map[string]json.RawMessage
}

// Request is the immutable semantic request shared by all text-generation
// codecs. Callers that need per-channel overrides must Clone first.
type Request struct {
	Model        ModelRef
	Conversation []InputItem
	Instructions []Instruction
	Tools        []ToolDefinition
	ToolPolicy   ToolPolicy
	Output       OutputConstraint
	Sampling     SamplingOptions
	Reasoning    ReasoningOptions
	Lifecycle    LifecycleOptions
	Metadata     MetadataOptions
	Vendor       VendorOptions
	Raw          RawRetention
}

// AnthropicOptionPath is the field path of the first Anthropic-only option
// the request carries (VendorOptions and the cache_control markers), for a
// bridge that cannot carry it to name.
func (r *Request) AnthropicOptionPath() (string, bool) {
	if r == nil {
		return "", false
	}
	if len(r.Vendor.CacheControl) > 0 {
		return "cache_control", true
	}
	if len(r.Vendor.Speed) > 0 {
		return "speed", true
	}
	for index, instruction := range r.Instructions {
		if len(instruction.CacheControl) > 0 {
			return "instructions[" + itoa(index) + "].cache_control", true
		}
	}
	for index, item := range r.Conversation {
		for partIndex, part := range item.Content {
			if len(part.CacheControl) > 0 {
				return "input[" + itoa(index) + "].content[" + itoa(partIndex) + "].cache_control", true
			}
		}
	}
	for index, tool := range r.Tools {
		if len(tool.CacheControl) > 0 {
			return "tools[" + itoa(index) + "].cache_control", true
		}
	}
	return "", false
}

func (r *Request) Clone() *Request {
	if r == nil {
		return nil
	}
	copyRequest := *r
	copyRequest.Conversation = append([]InputItem(nil), r.Conversation...)
	for i := range copyRequest.Conversation {
		copyRequest.Conversation[i].Content = append([]ContentPart(nil), r.Conversation[i].Content...)
		copyRequest.Conversation[i].Opaque = append([]OpaqueValue(nil), r.Conversation[i].Opaque...)
	}
	copyRequest.Instructions = append([]Instruction(nil), r.Instructions...)
	copyRequest.Tools = append([]ToolDefinition(nil), r.Tools...)
	copyRequest.Lifecycle.Include = append([]string(nil), r.Lifecycle.Include...)
	copyRequest.Raw.TopLevel = cloneRawMap(r.Raw.TopLevel)
	copyRequest.Raw.UnknownFields = cloneRawMap(r.Raw.UnknownFields)
	return &copyRequest
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if source == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}
