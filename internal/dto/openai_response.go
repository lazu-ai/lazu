package dto

import (
	"encoding/json"
	"fmt"

	"github.com/lazu-ai/lazu/pkg/billing"

	"github.com/lazu-ai/lazu/internal/types"
)

const (
	ResponsesOutputTypeImageGenerationCall = "image_generation_call"
	ResponsesOutputTypeFunctionCall        = "function_call"
)

type SimpleResponse struct {
	Usage `json:"usage"`
	Error any `json:"error"`
}

// GetOpenAIError 从动态错误类型中提取OpenAIError结构
func (s *SimpleResponse) GetOpenAIError() *types.OpenAIError {
	return GetOpenAIError(s.Error)
}

type OpenAITextResponseChoice struct {
	Index        int `json:"index"`
	Message      `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type OpenAITextResponse struct {
	Id      string                     `json:"id"`
	Model   string                     `json:"model"`
	Object  string                     `json:"object"`
	Created any                        `json:"created"`
	Choices []OpenAITextResponseChoice `json:"choices"`
	Error   any                        `json:"error,omitempty"`
	Usage   `json:"usage"`
}

// GetOpenAIError 从动态错误类型中提取OpenAIError结构
func (o *OpenAITextResponse) GetOpenAIError() *types.OpenAIError {
	return GetOpenAIError(o.Error)
}

type OpenAIEmbeddingResponseItem struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type OpenAIEmbeddingResponse struct {
	Object string                        `json:"object"`
	Data   []OpenAIEmbeddingResponseItem `json:"data"`
	Model  string                        `json:"model"`
	Usage  `json:"usage"`
}

type ChatCompletionsStreamResponseChoice struct {
	Delta        ChatCompletionsStreamResponseChoiceDelta `json:"delta,omitempty"`
	Logprobs     *any                                     `json:"logprobs"`
	FinishReason *string                                  `json:"finish_reason"`
	Index        int                                      `json:"index"`
}

type ChatCompletionsStreamResponseChoiceDelta struct {
	Content          *string            `json:"content,omitempty"`
	Refusal          *string            `json:"refusal,omitempty"`
	ReasoningContent *string            `json:"reasoning_content,omitempty"`
	Reasoning        *string            `json:"reasoning,omitempty"`
	Role             string             `json:"role,omitempty"`
	ToolCalls        []ToolCallResponse `json:"tool_calls,omitempty"`
}

func (c *ChatCompletionsStreamResponseChoiceDelta) SetContentString(s string) {
	c.Content = &s
}

func (c *ChatCompletionsStreamResponseChoiceDelta) GetContentString() string {
	if c.Content == nil {
		return ""
	}
	return *c.Content
}

func (c *ChatCompletionsStreamResponseChoiceDelta) GetReasoningContent() string {
	if c.ReasoningContent == nil && c.Reasoning == nil {
		return ""
	}
	if c.ReasoningContent != nil {
		return *c.ReasoningContent
	}
	return *c.Reasoning
}

func (c *ChatCompletionsStreamResponseChoiceDelta) SetReasoningContent(s string) {
	c.ReasoningContent = &s
	//c.Reasoning = &s
}

type ToolCallResponse struct {
	// Index is not nil only in chat completion chunk object
	Index        *int                  `json:"index,omitempty"`
	ID           string                `json:"id,omitempty"`
	Type         any                   `json:"type"`
	Function     FunctionResponse      `json:"function"`
	ExtraContent *ToolCallExtraContent `json:"extra_content,omitempty"`
}

func (c *ToolCallResponse) SetIndex(i int) {
	c.Index = &i
}

type FunctionResponse struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`
	// call function with arguments in JSON format
	Parameters any    `json:"parameters,omitempty"` // request
	Arguments  string `json:"arguments"`            // response
}

type ChatCompletionsStreamResponse struct {
	Id                string                                `json:"id"`
	Object            string                                `json:"object"`
	Created           int64                                 `json:"created"`
	Model             string                                `json:"model"`
	SystemFingerprint *string                               `json:"system_fingerprint"`
	Choices           []ChatCompletionsStreamResponseChoice `json:"choices"`
	Usage             *Usage                                `json:"usage"`
	Error             any                                   `json:"error,omitempty"`
}

func (c *ChatCompletionsStreamResponse) GetOpenAIError() *types.OpenAIError {
	if c == nil {
		return nil
	}
	return GetOpenAIError(c.Error)
}

func (c *ChatCompletionsStreamResponse) IsFinished() bool {
	if len(c.Choices) == 0 {
		return false
	}
	return c.Choices[0].FinishReason != nil && *c.Choices[0].FinishReason != ""
}

func (c *ChatCompletionsStreamResponse) IsToolCall() bool {
	if len(c.Choices) == 0 {
		return false
	}
	return len(c.Choices[0].Delta.ToolCalls) > 0
}

func (c *ChatCompletionsStreamResponse) GetFirstToolCall() *ToolCallResponse {
	if c.IsToolCall() {
		return &c.Choices[0].Delta.ToolCalls[0]
	}
	return nil
}

func (c *ChatCompletionsStreamResponse) ClearToolCalls() {
	if !c.IsToolCall() {
		return
	}
	for choiceIdx := range c.Choices {
		for callIdx := range c.Choices[choiceIdx].Delta.ToolCalls {
			c.Choices[choiceIdx].Delta.ToolCalls[callIdx].ID = ""
			c.Choices[choiceIdx].Delta.ToolCalls[callIdx].Type = nil
			c.Choices[choiceIdx].Delta.ToolCalls[callIdx].Function.Name = ""
			c.Choices[choiceIdx].Delta.ToolCalls[callIdx].ExtraContent = nil
		}
	}
}

func (c *ChatCompletionsStreamResponse) Copy() *ChatCompletionsStreamResponse {
	choices := make([]ChatCompletionsStreamResponseChoice, len(c.Choices))
	copy(choices, c.Choices)
	return &ChatCompletionsStreamResponse{
		Id:                c.Id,
		Object:            c.Object,
		Created:           c.Created,
		Model:             c.Model,
		SystemFingerprint: c.SystemFingerprint,
		Choices:           choices,
		Usage:             c.Usage,
	}
}

func (c *ChatCompletionsStreamResponse) GetSystemFingerprint() string {
	if c.SystemFingerprint == nil {
		return ""
	}
	return *c.SystemFingerprint
}

func (c *ChatCompletionsStreamResponse) SetSystemFingerprint(s string) {
	c.SystemFingerprint = &s
}

type CompletionsStreamResponse struct {
	Choices []struct {
		Text         string `json:"text"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type Usage struct {
	ImageUsage        *billing.ImageUsage `json:"-"`
	GeneratedImages   *int                `json:"generated_images,omitempty"`
	InputImages       *int                `json:"input_images,omitempty"`
	InputTokenSource  UsageSource         `json:"-"`
	OutputTokenSource UsageSource         `json:"-"`
	// PromptExcludesCache marks usage whose PromptTokens counts only the fresh
	// input, with cache reads and writes reported beside it (Anthropic's
	// input_tokens). OpenAI-style usage folds the cache into prompt_tokens, and
	// billing subtracts it back out; doing that to Anthropic usage billed the
	// input line as zero.
	PromptExcludesCache   bool `json:"-"`
	PromptTokens          int  `json:"prompt_tokens"`
	CompletionTokens      int  `json:"completion_tokens"`
	TotalTokens           int  `json:"total_tokens"`
	PromptCacheHitTokens  int  `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens int  `json:"prompt_cache_miss_tokens,omitempty"`

	PromptTokensDetails    InputTokenDetails   `json:"prompt_tokens_details"`
	CompletionTokenDetails OutputTokenDetails  `json:"completion_tokens_details"`
	InputTokens            int                 `json:"input_tokens,omitempty"`
	OutputTokens           int                 `json:"output_tokens,omitempty"`
	InputTokensDetails     *InputTokenDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails    *OutputTokenDetails `json:"output_tokens_details,omitempty"`
	// OpenRouter Params
	Cost any `json:"cost,omitempty"`
}

// ResponsesUsage is the protocol-specific wire shape for the Responses API.
// Usage remains the internal superset so parsing, billing, and accounting can
// normalize provider aliases without leaking both protocols to clients.
type ResponsesUsage struct {
	InputTokens         int                 `json:"input_tokens"`
	InputTokensDetails  *InputTokenDetails  `json:"input_tokens_details,omitempty"`
	OutputTokens        int                 `json:"output_tokens"`
	OutputTokensDetails *OutputTokenDetails `json:"output_tokens_details,omitempty"`
	TotalTokens         int                 `json:"total_tokens"`
	Cost                any                 `json:"cost,omitempty"`
}

func ProjectResponsesUsage(usage *Usage) *ResponsesUsage {
	if usage == nil {
		return nil
	}
	inputTokens := usage.InputTokens
	if inputTokens == 0 {
		inputTokens = usage.PromptTokens
	}
	outputTokens := usage.OutputTokens
	if outputTokens == 0 {
		outputTokens = usage.CompletionTokens
	}
	inputDetails := usage.InputTokensDetails
	if inputDetails == nil {
		copyDetails := usage.PromptTokensDetails
		inputDetails = &copyDetails
	}
	outputDetails := usage.OutputTokensDetails
	if outputDetails == nil {
		copyDetails := usage.CompletionTokenDetails
		outputDetails = &copyDetails
	}
	totalTokens := usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	return &ResponsesUsage{
		InputTokens:         inputTokens,
		InputTokensDetails:  inputDetails,
		OutputTokens:        outputTokens,
		OutputTokensDetails: outputDetails,
		TotalTokens:         totalTokens,
		Cost:                usage.Cost,
	}
}

// ProjectChatUsage removes Responses-only aliases from the Chat wire shape.
// It intentionally returns the existing Usage type because the Chat DTOs embed
// it, while the Responses-only fields have omitempty tags.
func ProjectChatUsage(usage *Usage) *Usage {
	if usage == nil {
		return nil
	}
	projected := *usage
	if projected.PromptTokens == 0 {
		projected.PromptTokens = projected.InputTokens
	}
	if projected.CompletionTokens == 0 {
		projected.CompletionTokens = projected.OutputTokens
	}
	if projected.PromptTokensDetails == (InputTokenDetails{}) && projected.InputTokensDetails != nil {
		projected.PromptTokensDetails = *projected.InputTokensDetails
	}
	if projected.CompletionTokenDetails == (OutputTokenDetails{}) && projected.OutputTokensDetails != nil {
		projected.CompletionTokenDetails = *projected.OutputTokensDetails
	}
	if projected.TotalTokens == 0 {
		projected.TotalTokens = projected.PromptTokens + projected.CompletionTokens
	}
	projected.InputTokens = 0
	projected.OutputTokens = 0
	projected.InputTokensDetails = nil
	projected.OutputTokensDetails = nil
	return &projected
}

type CachedTokenDetails struct {
	TextTokens  int `json:"text_tokens"`
	AudioTokens int `json:"audio_tokens"`
	ImageTokens int `json:"image_tokens"`
}

type InputTokenDetails struct {
	CachedTokens           int                `json:"-"`
	CachedTokensDetails    CachedTokenDetails `json:"-"`
	CacheWriteTokens       int                `json:"-"`
	CacheMissTokens        int                `json:"-"`
	CachedCreationTokens   int                `json:"-"`
	CachedCreation5mTokens int                `json:"-"`
	CachedCreation1hTokens int                `json:"-"`
	TextTokens             int                `json:"-"`
	AudioTokens            int                `json:"-"`
	ImageTokens            int                `json:"-"`
}

func (d InputTokenDetails) MarshalJSON() ([]byte, error) {
	cacheWriteTokens := d.CacheWriteTokens
	if cacheWriteTokens == 0 {
		cacheWriteTokens = d.CachedCreationTokens
	}
	if cacheWriteTokens == 0 {
		cacheWriteTokens = d.CachedCreation5mTokens + d.CachedCreation1hTokens
	}
	type inputTokenDetailsJSON struct {
		CachedTokens        int                 `json:"cached_tokens"`
		CachedTokensDetails *CachedTokenDetails `json:"cached_tokens_details,omitempty"`
		CacheWriteTokens    int                 `json:"cache_write_tokens,omitempty"`
		CacheWrite5mTokens  int                 `json:"cache_write_5m_tokens,omitempty"`
		CacheWrite1hTokens  int                 `json:"cache_write_1h_tokens,omitempty"`
		CacheMissTokens     int                 `json:"cache_miss_tokens,omitempty"`
		TextTokens          int                 `json:"text_tokens"`
		AudioTokens         int                 `json:"audio_tokens"`
		ImageTokens         int                 `json:"image_tokens"`
	}
	var cachedTokensDetails *CachedTokenDetails
	if d.CachedTokensDetails != (CachedTokenDetails{}) {
		cachedDetailsCopy := d.CachedTokensDetails
		cachedTokensDetails = &cachedDetailsCopy
	}
	return json.Marshal(inputTokenDetailsJSON{
		CachedTokens:        d.CachedTokens,
		CachedTokensDetails: cachedTokensDetails,
		CacheWriteTokens:    cacheWriteTokens,
		CacheWrite5mTokens:  d.CachedCreation5mTokens,
		CacheWrite1hTokens:  d.CachedCreation1hTokens,
		CacheMissTokens:     d.CacheMissTokens,
		TextTokens:          d.TextTokens,
		AudioTokens:         d.AudioTokens,
		ImageTokens:         d.ImageTokens,
	})
}

func (d *InputTokenDetails) UnmarshalJSON(data []byte) error {
	type inputTokenDetailsJSON struct {
		CachedTokens             int                `json:"cached_tokens"`
		CachedTokensDetails      CachedTokenDetails `json:"cached_tokens_details"`
		CachedTokenDetails       CachedTokenDetails `json:"cached_token_details"`
		CacheWriteTokens         int                `json:"cache_write_tokens"`
		CacheWrite5mTokens       int                `json:"cache_write_5m_tokens"`
		CacheWrite1hTokens       int                `json:"cache_write_1h_tokens"`
		CacheMissTokens          int                `json:"cache_miss_tokens"`
		CacheCreationInputTokens int                `json:"cache_creation_input_tokens"`
		CachedCreationTokens     int                `json:"cached_creation_tokens"`
		CachedCreation5mTokens   int                `json:"cached_creation_5m_tokens"`
		CachedCreation1hTokens   int                `json:"cached_creation_1h_tokens"`
		TextTokens               int                `json:"text_tokens"`
		AudioTokens              int                `json:"audio_tokens"`
		ImageTokens              int                `json:"image_tokens"`
	}
	var payload inputTokenDetailsJSON
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	d.CachedTokens = payload.CachedTokens
	d.CachedTokensDetails = payload.CachedTokensDetails
	if d.CachedTokensDetails == (CachedTokenDetails{}) {
		d.CachedTokensDetails = payload.CachedTokenDetails
	}
	d.CacheWriteTokens = payload.CacheWriteTokens
	d.CacheMissTokens = payload.CacheMissTokens
	d.CachedCreationTokens = firstPositiveInt(
		payload.CacheCreationInputTokens,
		payload.CachedCreationTokens,
		payload.CacheWriteTokens,
	)
	d.CachedCreation5mTokens = firstPositiveInt(payload.CachedCreation5mTokens, payload.CacheWrite5mTokens)
	d.CachedCreation1hTokens = firstPositiveInt(payload.CachedCreation1hTokens, payload.CacheWrite1hTokens)
	d.TextTokens = payload.TextTokens
	d.AudioTokens = payload.AudioTokens
	d.ImageTokens = payload.ImageTokens
	return nil
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

type OutputTokenDetails struct {
	TextTokens               int `json:"text_tokens"`
	AudioTokens              int `json:"audio_tokens"`
	ImageTokens              int `json:"image_tokens"`
	ReasoningTokens          int `json:"reasoning_tokens"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
}

type OpenAIResponsesResponse struct {
	ID                 string             `json:"id"`
	Object             string             `json:"object"`
	CreatedAt          int                `json:"created_at"`
	Status             string             `json:"status"`
	Error              any                `json:"error,omitempty"`
	IncompleteDetails  *IncompleteDetails `json:"incomplete_details,omitempty"`
	Instructions       json.RawMessage    `json:"instructions"`
	MaxOutputTokens    *int               `json:"max_output_tokens"`
	Model              string             `json:"model"`
	Output             []ResponsesOutput  `json:"output"`
	ParallelToolCalls  bool               `json:"parallel_tool_calls"`
	PreviousResponseID string             `json:"previous_response_id"`
	Reasoning          *Reasoning         `json:"reasoning"`
	Store              bool               `json:"store"`
	Temperature        float64            `json:"temperature"`
	ToolChoice         json.RawMessage    `json:"tool_choice"`
	Tools              []map[string]any   `json:"tools"`
	TopP               float64            `json:"top_p"`
	Truncation         string             `json:"truncation"`
	Usage              *Usage             `json:"usage"`
	User               json.RawMessage    `json:"user"`
	Metadata           json.RawMessage    `json:"metadata"`
}

func (o OpenAIResponsesResponse) MarshalJSON() ([]byte, error) {
	type responseAlias OpenAIResponsesResponse
	return json.Marshal(struct {
		*responseAlias
		Usage *ResponsesUsage `json:"usage"`
	}{
		responseAlias: (*responseAlias)(&o),
		Usage:         ProjectResponsesUsage(o.Usage),
	})
}

// GetOpenAIError 从动态错误类型中提取OpenAIError结构
func (o *OpenAIResponsesResponse) GetOpenAIError() *types.OpenAIError {
	return GetOpenAIError(o.Error)
}

func (o *OpenAIResponsesResponse) HasImageGenerationCall() bool {
	if len(o.Output) == 0 {
		return false
	}
	for _, output := range o.Output {
		if output.Type == ResponsesOutputTypeImageGenerationCall {
			return true
		}
	}
	return false
}

func (o *OpenAIResponsesResponse) GetQuality() string {
	if len(o.Output) == 0 {
		return ""
	}
	for _, output := range o.Output {
		if output.Type == ResponsesOutputTypeImageGenerationCall {
			return output.Quality
		}
	}
	return ""
}

func (o *OpenAIResponsesResponse) GetSize() string {
	if len(o.Output) == 0 {
		return ""
	}
	for _, output := range o.Output {
		if output.Type == ResponsesOutputTypeImageGenerationCall {
			return output.Size
		}
	}
	return ""
}

type IncompleteDetails struct {
	Reason    string `json:"reason,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

type ResponsesOutput struct {
	Type      string                   `json:"type"`
	ID        string                   `json:"id"`
	Status    string                   `json:"status"`
	Action    *ResponsesOutputAction   `json:"action,omitempty"`
	Role      string                   `json:"role"`
	Content   []ResponsesOutputContent `json:"content"`
	Quality   string                   `json:"quality"`
	Size      string                   `json:"size"`
	CallID    string                   `json:"call_id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
}

// MarshalJSON emits fields for each item kind. Inert image/message fields on
// function calls otherwise become unknown semantics when clients replay output.
func (o ResponsesOutput) MarshalJSON() ([]byte, error) {
	switch o.Type {
	case "reasoning":
		return json.Marshal(NewResponsesReasoningItem(o.ID, o.Status, o.Content))
	case "message":
		return json.Marshal(struct {
			Type    string                   `json:"type"`
			ID      string                   `json:"id"`
			Status  string                   `json:"status"`
			Role    string                   `json:"role"`
			Content []ResponsesOutputContent `json:"content"`
		}{o.Type, o.ID, o.Status, o.Role, o.Content})
	case ResponsesOutputTypeFunctionCall:
		return json.Marshal(struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Status    string `json:"status"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{o.Type, o.ID, o.Status, o.CallID, o.Name, o.Arguments})
	}
	type responsesOutputAlias ResponsesOutput
	return json.Marshal(responsesOutputAlias(o))
}

type ResponsesOutputAction struct {
	Type string `json:"type,omitempty"`
}

type ResponsesOutputContent struct {
	Type        string        `json:"type"`
	Text        string        `json:"text"`
	Refusal     string        `json:"refusal,omitempty"`
	Annotations []interface{} `json:"annotations"`
}

func (c ResponsesOutputContent) MarshalJSON() ([]byte, error) {
	if c.Type == "refusal" {
		return json.Marshal(struct {
			Type    string `json:"type"`
			Refusal string `json:"refusal"`
		}{Type: c.Type, Refusal: c.Refusal})
	}
	if c.Type == "reasoning_text" {
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: c.Type, Text: c.Text})
	}
	type responsesOutputContentAlias ResponsesOutputContent
	if c.Type == "output_text" && c.Annotations == nil {
		c.Annotations = []interface{}{}
	}
	return json.Marshal(responsesOutputContentAlias(c))
}

const (
	BuildInToolWebSearch        = "web_search"
	BuildInToolWebSearchPreview = "web_search_preview"
	BuildInToolFileSearch       = "file_search"
)

const (
	BuildInCallWebSearchCall  = "web_search_call"
	BuildInCallFileSearchCall = "file_search_call"
)

const (
	ResponsesOutputTypeItemAdded = "response.output_item.added"
	ResponsesOutputTypeItemDone  = "response.output_item.done"
)

// ResponsesStreamResponse 用于处理 /v1/responses 流式响应
type ResponsesStreamResponse struct {
	Type        string                   `json:"type"`
	Response    *OpenAIResponsesResponse `json:"response,omitempty"`
	Delta       string                   `json:"delta,omitempty"`
	Item        *ResponsesOutput         `json:"item,omitempty"`
	ItemID      string                   `json:"item_id,omitempty"`
	OutputIndex *int                     `json:"output_index,omitempty"`
	Error       any                      `json:"error,omitempty"`
	Code        any                      `json:"code,omitempty"`
	Message     string                   `json:"message,omitempty"`
	Param       string                   `json:"param,omitempty"`
}

func (r *ResponsesStreamResponse) GetOpenAIError() *types.OpenAIError {
	if r == nil {
		return nil
	}
	if upstreamErr := GetOpenAIError(r.Error); upstreamErr != nil {
		return upstreamErr
	}
	if r.Type != "error" && r.Code == nil && r.Message == "" {
		return nil
	}
	return &types.OpenAIError{Type: "upstream_error", Code: r.Code, Message: r.Message, Param: r.Param}
}

// GetOpenAIError 从动态错误类型中提取OpenAIError结构
func GetOpenAIError(errorField any) *types.OpenAIError {
	if errorField == nil {
		return nil
	}

	switch err := errorField.(type) {
	case types.OpenAIError:
		return &err
	case *types.OpenAIError:
		return err
	case map[string]interface{}:
		// 处理从JSON解析来的map结构
		openaiErr := &types.OpenAIError{}
		if errType, ok := err["type"].(string); ok {
			openaiErr.Type = errType
		}
		if errMsg, ok := err["message"].(string); ok {
			openaiErr.Message = errMsg
		}
		if errParam, ok := err["param"].(string); ok {
			openaiErr.Param = errParam
		}
		if errCode, ok := err["code"]; ok {
			openaiErr.Code = errCode
		}
		return openaiErr
	case string:
		// 处理简单字符串错误
		return &types.OpenAIError{
			Type:    "error",
			Message: err,
		}
	default:
		// 未知类型，尝试转换为字符串
		return &types.OpenAIError{
			Type:    "unknown_error",
			Message: fmt.Sprintf("%v", err),
		}
	}
}
