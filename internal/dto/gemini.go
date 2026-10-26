package dto

import (
	"encoding/json"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/logger"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

type GeminiChatRequest struct {
	Requests           []GeminiChatRequest        `json:"requests,omitempty"` // For batch requests
	Contents           []GeminiChatContent        `json:"contents"`
	SafetySettings     []GeminiChatSafetySettings `json:"safetySettings,omitempty"`
	GenerationConfig   GeminiChatGenerationConfig `json:"generationConfig,omitempty"`
	Tools              json.RawMessage            `json:"tools,omitempty"`
	ToolConfig         *ToolConfig                `json:"toolConfig,omitempty"`
	SystemInstructions *GeminiChatContent         `json:"systemInstruction,omitempty"`
	CachedContent      string                     `json:"cachedContent,omitempty"`
}

type ToolConfig struct {
	FunctionCallingConfig *FunctionCallingConfig `json:"functionCallingConfig,omitempty"`
	RetrievalConfig       *RetrievalConfig       `json:"retrievalConfig,omitempty"`
}

type FunctionCallingConfig struct {
	Mode                 FunctionCallingConfigMode `json:"mode,omitempty"`
	AllowedFunctionNames []string                  `json:"allowedFunctionNames,omitempty"`
}
type FunctionCallingConfigMode string

type RetrievalConfig struct {
	LatLng       *LatLng `json:"latLng,omitempty"`
	LanguageCode string  `json:"languageCode,omitempty"`
}

type LatLng struct {
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

func (r *GeminiChatRequest) UnmarshalJSON(data []byte) error {
	type Alias GeminiChatRequest
	var aux Alias
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}
	*r = GeminiChatRequest(aux)

	var raw map[string]json.RawMessage
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}

	if _, ok := raw["generationConfig"]; !ok {
		if rawGenerationConfig, ok := raw["generation_config"]; ok && len(rawGenerationConfig) > 0 {
			var generationConfig GeminiChatGenerationConfig
			if err := common.Unmarshal(rawGenerationConfig, &generationConfig); err == nil {
				r.GenerationConfig = generationConfig
			}
		}
	}

	if _, ok := raw["safetySettings"]; !ok {
		if rawSafetySettings, ok := raw["safety_settings"]; ok && len(rawSafetySettings) > 0 {
			var safetySettings []GeminiChatSafetySettings
			if err := common.Unmarshal(rawSafetySettings, &safetySettings); err == nil {
				r.SafetySettings = safetySettings
			}
		}
	}

	if r.ToolConfig == nil {
		if rawToolConfig, ok := raw["tool_config"]; ok && len(rawToolConfig) > 0 {
			var toolConfig ToolConfig
			if err := common.Unmarshal(rawToolConfig, &toolConfig); err == nil {
				r.ToolConfig = &toolConfig
			}
		}
	}

	if r.SystemInstructions == nil {
		if rawSystemInstruction, ok := raw["system_instruction"]; ok && len(rawSystemInstruction) > 0 {
			var content GeminiChatContent
			if err := common.Unmarshal(rawSystemInstruction, &content); err == nil {
				r.SystemInstructions = &content
			}
		}
	}

	if r.CachedContent == "" {
		if _, ok := raw["cachedContent"]; !ok {
			if rawCachedContent, ok := raw["cached_content"]; ok && len(rawCachedContent) > 0 {
				var cachedContent string
				if err := common.Unmarshal(rawCachedContent, &cachedContent); err == nil {
					r.CachedContent = cachedContent
				}
			}
		}
	}

	return nil
}

func (c *ToolConfig) UnmarshalJSON(data []byte) error {
	type Alias ToolConfig
	var aux struct {
		Alias
		FunctionCallingConfigSnake *FunctionCallingConfig `json:"function_calling_config,omitempty"`
		RetrievalConfigSnake       *RetrievalConfig       `json:"retrieval_config,omitempty"`
	}
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = ToolConfig(aux.Alias)

	if c.FunctionCallingConfig == nil && aux.FunctionCallingConfigSnake != nil {
		c.FunctionCallingConfig = aux.FunctionCallingConfigSnake
	}
	if c.RetrievalConfig == nil && aux.RetrievalConfigSnake != nil {
		c.RetrievalConfig = aux.RetrievalConfigSnake
	}
	return nil
}

func (c *FunctionCallingConfig) UnmarshalJSON(data []byte) error {
	type Alias FunctionCallingConfig
	var aux struct {
		Alias
		AllowedFunctionNamesSnake []string `json:"allowed_function_names,omitempty"`
	}
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = FunctionCallingConfig(aux.Alias)
	if len(c.AllowedFunctionNames) == 0 && len(aux.AllowedFunctionNamesSnake) > 0 {
		c.AllowedFunctionNames = aux.AllowedFunctionNamesSnake
	}
	return nil
}

func (c *RetrievalConfig) UnmarshalJSON(data []byte) error {
	type Alias RetrievalConfig
	var aux struct {
		Alias
		LatLngSnake       *LatLng `json:"lat_lng,omitempty"`
		LanguageCodeSnake string  `json:"language_code,omitempty"`
	}
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = RetrievalConfig(aux.Alias)
	if c.LatLng == nil && aux.LatLngSnake != nil {
		c.LatLng = aux.LatLngSnake
	}
	if c.LanguageCode == "" && aux.LanguageCodeSnake != "" {
		c.LanguageCode = aux.LanguageCodeSnake
	}
	return nil
}

func (r *GeminiChatRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var files []*types.FileMeta = make([]*types.FileMeta, 0)

	var maxTokens int

	if r.GenerationConfig.MaxOutputTokens > 0 {
		maxTokens = int(r.GenerationConfig.MaxOutputTokens)
	}

	var inputTexts []string
	for _, content := range r.Contents {
		for _, part := range content.Parts {
			if part.Text != "" {
				inputTexts = append(inputTexts, part.Text)
			}
			if part.InlineData != nil && part.InlineData.Data != "" {
				if strings.HasPrefix(part.InlineData.MimeType, "image/") {
					files = append(files, &types.FileMeta{
						FileType:   types.FileTypeImage,
						OriginData: part.InlineData.Data,
					})
				} else if strings.HasPrefix(part.InlineData.MimeType, "audio/") {
					files = append(files, &types.FileMeta{
						FileType:   types.FileTypeAudio,
						OriginData: part.InlineData.Data,
					})
				} else if strings.HasPrefix(part.InlineData.MimeType, "video/") {
					files = append(files, &types.FileMeta{
						FileType:   types.FileTypeVideo,
						OriginData: part.InlineData.Data,
					})
				} else {
					files = append(files, &types.FileMeta{
						FileType:   types.FileTypeFile,
						OriginData: part.InlineData.Data,
					})
				}
			}
		}
	}

	inputText := strings.Join(inputTexts, "\n")
	return &types.TokenCountMeta{
		CombineText:     inputText,
		Files:           files,
		MaxTokens:       maxTokens,
		CompletionCount: r.GenerationConfig.CandidateCount,
	}
}

func (r *GeminiChatRequest) IsStream(c *gin.Context) bool {
	return IsGeminiStreamRequest(c)
}

// IsGeminiStreamRequest is the one stream predicate for Gemini's two wire
// forms: the streaming action and generateContent negotiated as SSE.
func IsGeminiStreamRequest(c *gin.Context) bool {
	return c != nil && c.Request != nil &&
		(strings.Contains(c.Request.URL.Path, ":streamGenerateContent") || c.Query("alt") == "sse")
}

func (r *GeminiChatRequest) SetModelName(modelName string) {
	// GeminiChatRequest does not have a model field, so this method does nothing.
}

func (r *GeminiChatRequest) GetTools() []GeminiChatTool {
	var tools []GeminiChatTool
	switch common.GetJsonType(r.Tools) {
	case "array":
		if err := common.Unmarshal(r.Tools, &tools); err != nil {
			logger.LogError(nil, "error_unmarshalling_tools: "+err.Error())
			return nil
		}
	case "object":
		singleTool := GeminiChatTool{}
		if err := common.Unmarshal(r.Tools, &singleTool); err != nil {
			logger.LogError(nil, "error_unmarshalling_single_tool: "+err.Error())
			return nil
		}
		tools = []GeminiChatTool{singleTool}
	}
	return tools
}

func (r *GeminiChatRequest) SetTools(tools []GeminiChatTool) {
	if len(tools) == 0 {
		r.Tools = json.RawMessage("[]")
		return
	}

	// Marshal the tools to JSON
	data, err := common.Marshal(tools)
	if err != nil {
		logger.LogError(nil, "error_marshalling_tools: "+err.Error())
		return
	}
	r.Tools = data
}

func (r *GeminiChatRequest) HasThoughtSignature() bool {
	for _, content := range r.Contents {
		for _, part := range content.Parts {
			if part.ThoughtSignature != "" {
				return true
			}
		}
	}
	return false
}

type GeminiThinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

func (c *GeminiThinkingConfig) UnmarshalJSON(data []byte) error {
	type Alias GeminiThinkingConfig
	var aux Alias
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = GeminiThinkingConfig(aux)

	var raw map[string]json.RawMessage
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}

	if _, ok := raw["includeThoughts"]; !ok {
		if rawIncludeThoughts, ok := raw["include_thoughts"]; ok && len(rawIncludeThoughts) > 0 {
			_ = common.Unmarshal(rawIncludeThoughts, &c.IncludeThoughts)
		}
	}

	if _, ok := raw["thinkingBudget"]; !ok {
		if rawThinkingBudget, ok := raw["thinking_budget"]; ok && len(rawThinkingBudget) > 0 {
			_ = common.Unmarshal(rawThinkingBudget, &c.ThinkingBudget)
		}
	}

	if _, ok := raw["thinkingLevel"]; !ok {
		if rawThinkingLevel, ok := raw["thinking_level"]; ok && len(rawThinkingLevel) > 0 {
			_ = common.Unmarshal(rawThinkingLevel, &c.ThinkingLevel)
		}
	}

	return nil
}

func (c *GeminiThinkingConfig) SetThinkingBudget(budget int) {
	c.ThinkingBudget = &budget
}

type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// UnmarshalJSON custom unmarshaler for GeminiInlineData to support snake_case and camelCase for MimeType
func (g *GeminiInlineData) UnmarshalJSON(data []byte) error {
	type Alias GeminiInlineData // Use type alias to avoid recursion
	var aux struct {
		Alias
		MimeTypeSnake string `json:"mime_type"`
	}

	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}

	*g = GeminiInlineData(aux.Alias) // Copy other fields if any in future

	// Prioritize snake_case if present
	if aux.MimeTypeSnake != "" {
		g.MimeType = aux.MimeTypeSnake
	} else if aux.MimeType != "" { // Fallback to camelCase from Alias
		g.MimeType = aux.MimeType
	}
	// g.Data would be populated by aux.Alias.Data
	return nil
}

type FunctionCall struct {
	ID           string `json:"id,omitempty"`
	FunctionName string `json:"name"`
	Arguments    any    `json:"args"`
}

type GeminiFunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type GeminiPartExecutableCode struct {
	Language string `json:"language,omitempty"`
	Code     string `json:"code,omitempty"`
}

type GeminiPartCodeExecutionResult struct {
	Outcome string `json:"outcome,omitempty"`
	Output  string `json:"output,omitempty"`
}

type GeminiFileData struct {
	MimeType string `json:"mimeType,omitempty"`
	FileUri  string `json:"fileUri,omitempty"`
}

func (g *GeminiFileData) UnmarshalJSON(data []byte) error {
	type Alias GeminiFileData
	var aux struct {
		Alias
		MimeTypeSnake string `json:"mime_type,omitempty"`
		FileUriSnake  string `json:"file_uri,omitempty"`
	}
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}

	*g = GeminiFileData(aux.Alias)
	if g.MimeType == "" && aux.MimeTypeSnake != "" {
		g.MimeType = aux.MimeTypeSnake
	}
	if g.FileUri == "" && aux.FileUriSnake != "" {
		g.FileUri = aux.FileUriSnake
	}
	return nil
}

type GeminiPart struct {
	Text                string                         `json:"text,omitempty"`
	Thought             bool                           `json:"thought,omitempty"`
	ThoughtSignature    string                         `json:"thoughtSignature,omitempty"`
	InlineData          *GeminiInlineData              `json:"inlineData,omitempty"`
	FunctionCall        *FunctionCall                  `json:"functionCall,omitempty"`
	FunctionResponse    *GeminiFunctionResponse        `json:"functionResponse,omitempty"`
	FileData            *GeminiFileData                `json:"fileData,omitempty"`
	ExecutableCode      *GeminiPartExecutableCode      `json:"executableCode,omitempty"`
	CodeExecutionResult *GeminiPartCodeExecutionResult `json:"codeExecutionResult,omitempty"`
}

// UnmarshalJSON custom unmarshaler for GeminiPart to support snake_case and camelCase.
func (p *GeminiPart) UnmarshalJSON(data []byte) error {
	// Alias to avoid recursion during unmarshalling
	type Alias GeminiPart
	var aux Alias
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Assign fields from alias
	*p = GeminiPart(aux)

	var raw map[string]json.RawMessage
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}

	if rawInline := pickRawMessage(raw, "inlineData", "inline_data"); len(rawInline) > 0 && common.GetJsonType(rawInline) != "null" {
		var inlineData GeminiInlineData
		if err := common.Unmarshal(rawInline, &inlineData); err == nil {
			p.InlineData = &inlineData
		}
	}

	if p.ThoughtSignature == "" {
		if rawThoughtSignature := pickRawMessage(raw, "thoughtSignature", "thought_signature"); len(rawThoughtSignature) > 0 && common.GetJsonType(rawThoughtSignature) != "null" {
			_ = common.Unmarshal(rawThoughtSignature, &p.ThoughtSignature)
		}
	}

	if rawFunctionCall := pickRawMessage(raw, "functionCall", "function_call"); len(rawFunctionCall) > 0 && common.GetJsonType(rawFunctionCall) != "null" {
		var functionCall FunctionCall
		if err := common.Unmarshal(rawFunctionCall, &functionCall); err == nil {
			p.FunctionCall = &functionCall
		}
	}

	if rawFunctionResponse := pickRawMessage(raw, "functionResponse", "function_response"); len(rawFunctionResponse) > 0 && common.GetJsonType(rawFunctionResponse) != "null" {
		var functionResponse GeminiFunctionResponse
		if err := common.Unmarshal(rawFunctionResponse, &functionResponse); err == nil {
			p.FunctionResponse = &functionResponse
		}
	}

	if rawFileData := pickRawMessage(raw, "fileData", "file_data"); len(rawFileData) > 0 && common.GetJsonType(rawFileData) != "null" {
		var fileData GeminiFileData
		if err := common.Unmarshal(rawFileData, &fileData); err == nil {
			p.FileData = &fileData
		}
	}

	if rawExecutableCode := pickRawMessage(raw, "executableCode", "executable_code"); len(rawExecutableCode) > 0 && common.GetJsonType(rawExecutableCode) != "null" {
		var executableCode GeminiPartExecutableCode
		if err := common.Unmarshal(rawExecutableCode, &executableCode); err == nil {
			p.ExecutableCode = &executableCode
		}
	}

	if rawCodeExecutionResult := pickRawMessage(raw, "codeExecutionResult", "code_execution_result"); len(rawCodeExecutionResult) > 0 && common.GetJsonType(rawCodeExecutionResult) != "null" {
		var codeExecutionResult GeminiPartCodeExecutionResult
		if err := common.Unmarshal(rawCodeExecutionResult, &codeExecutionResult); err == nil {
			p.CodeExecutionResult = &codeExecutionResult
		}
	}

	return nil
}

type GeminiChatContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiChatSafetySettings struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type GeminiChatTool struct {
	GoogleSearch          any `json:"googleSearch,omitempty"`
	GoogleSearchRetrieval any `json:"googleSearchRetrieval,omitempty"`
	CodeExecution         any `json:"codeExecution,omitempty"`
	FunctionDeclarations  any `json:"functionDeclarations,omitempty"`
	URLContext            any `json:"urlContext,omitempty"`
}

func (t *GeminiChatTool) UnmarshalJSON(data []byte) error {
	*t = GeminiChatTool{}
	var raw map[string]json.RawMessage
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}

	if rawSearch := pickRawMessage(raw, "googleSearch", "google_search"); len(rawSearch) > 0 {
		var parsed any
		if err := common.Unmarshal(rawSearch, &parsed); err == nil {
			t.GoogleSearch = parsed
		}
	}

	if rawSearchRetrieval := pickRawMessage(raw, "googleSearchRetrieval", "google_search_retrieval"); len(rawSearchRetrieval) > 0 {
		var parsed any
		if err := common.Unmarshal(rawSearchRetrieval, &parsed); err == nil {
			t.GoogleSearchRetrieval = parsed
		}
	}

	if rawCodeExecution := pickRawMessage(raw, "codeExecution", "code_execution"); len(rawCodeExecution) > 0 {
		var parsed any
		if err := common.Unmarshal(rawCodeExecution, &parsed); err == nil {
			t.CodeExecution = parsed
		}
	}

	if rawURLContext := pickRawMessage(raw, "urlContext", "url_context"); len(rawURLContext) > 0 {
		var parsed any
		if err := common.Unmarshal(rawURLContext, &parsed); err == nil {
			t.URLContext = parsed
		}
	}

	if rawFunctions := pickRawMessage(raw, "functionDeclarations", "function_declarations"); len(rawFunctions) > 0 {
		var declarations []FunctionRequest
		if err := common.Unmarshal(rawFunctions, &declarations); err == nil {
			t.FunctionDeclarations = declarations
			return nil
		}
		var declaration FunctionRequest
		if err := common.Unmarshal(rawFunctions, &declaration); err == nil {
			t.FunctionDeclarations = []FunctionRequest{declaration}
			return nil
		}
		var parsed any
		if err := common.Unmarshal(rawFunctions, &parsed); err == nil {
			t.FunctionDeclarations = parsed
		}
	}

	return nil
}

func pickRawMessage(raw map[string]json.RawMessage, camelKey string, snakeKey string) json.RawMessage {
	if value, ok := raw[camelKey]; ok && len(value) > 0 {
		return value
	}
	if value, ok := raw[snakeKey]; ok && len(value) > 0 {
		return value
	}
	return nil
}

type GeminiChatGenerationConfig struct {
	Temperature                *float64              `json:"temperature,omitempty"`
	TopP                       float64               `json:"topP,omitempty"`
	TopK                       float64               `json:"topK,omitempty"`
	MaxOutputTokens            uint                  `json:"maxOutputTokens,omitempty"`
	CandidateCount             int                   `json:"candidateCount,omitempty"`
	StopSequences              []string              `json:"stopSequences,omitempty"`
	ResponseMimeType           string                `json:"responseMimeType,omitempty"`
	ResponseSchema             any                   `json:"responseSchema,omitempty"`
	ResponseJsonSchema         json.RawMessage       `json:"responseJsonSchema,omitempty"`
	PresencePenalty            *float32              `json:"presencePenalty,omitempty"`
	FrequencyPenalty           *float32              `json:"frequencyPenalty,omitempty"`
	ResponseLogprobs           bool                  `json:"responseLogprobs,omitempty"`
	Logprobs                   *int32                `json:"logprobs,omitempty"`
	EnableEnhancedCivicAnswers *bool                 `json:"enableEnhancedCivicAnswers,omitempty"`
	MediaResolution            MediaResolution       `json:"mediaResolution,omitempty"`
	Seed                       int64                 `json:"seed,omitempty"`
	ResponseModalities         []string              `json:"responseModalities,omitempty"`
	ThinkingConfig             *GeminiThinkingConfig `json:"thinkingConfig,omitempty"`
	SpeechConfig               json.RawMessage       `json:"speechConfig,omitempty"` // RawMessage to allow flexible speech config
	ImageConfig                json.RawMessage       `json:"imageConfig,omitempty"`  // RawMessage to allow flexible image config
}

func (c *GeminiChatGenerationConfig) UnmarshalJSON(data []byte) error {
	type Alias GeminiChatGenerationConfig
	var aux Alias
	if err := common.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = GeminiChatGenerationConfig(aux)

	var raw map[string]json.RawMessage
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}

	if _, ok := raw["topP"]; !ok {
		if rawTopP, ok := raw["top_p"]; ok && len(rawTopP) > 0 {
			_ = common.Unmarshal(rawTopP, &c.TopP)
		}
	}

	if _, ok := raw["topK"]; !ok {
		if rawTopK, ok := raw["top_k"]; ok && len(rawTopK) > 0 {
			_ = common.Unmarshal(rawTopK, &c.TopK)
		}
	}

	if _, ok := raw["maxOutputTokens"]; !ok {
		if rawMaxOutputTokens, ok := raw["max_output_tokens"]; ok && len(rawMaxOutputTokens) > 0 {
			_ = common.Unmarshal(rawMaxOutputTokens, &c.MaxOutputTokens)
		}
	}

	if _, ok := raw["candidateCount"]; !ok {
		if rawCandidateCount, ok := raw["candidate_count"]; ok && len(rawCandidateCount) > 0 {
			_ = common.Unmarshal(rawCandidateCount, &c.CandidateCount)
		}
	}

	if _, ok := raw["stopSequences"]; !ok {
		if rawStopSequences, ok := raw["stop_sequences"]; ok && len(rawStopSequences) > 0 {
			_ = common.Unmarshal(rawStopSequences, &c.StopSequences)
		}
	}

	if _, ok := raw["responseMimeType"]; !ok {
		if rawResponseMimeType, ok := raw["response_mime_type"]; ok && len(rawResponseMimeType) > 0 {
			_ = common.Unmarshal(rawResponseMimeType, &c.ResponseMimeType)
		}
	}

	if _, ok := raw["responseSchema"]; !ok {
		if rawResponseSchema, ok := raw["response_schema"]; ok && len(rawResponseSchema) > 0 {
			var responseSchema any
			if err := common.Unmarshal(rawResponseSchema, &responseSchema); err == nil {
				c.ResponseSchema = responseSchema
			}
		}
	}

	if _, ok := raw["responseJsonSchema"]; !ok {
		if rawResponseJsonSchema, ok := raw["response_json_schema"]; ok && len(rawResponseJsonSchema) > 0 {
			c.ResponseJsonSchema = rawResponseJsonSchema
		}
	}

	if _, ok := raw["presencePenalty"]; !ok {
		if rawPresencePenalty, ok := raw["presence_penalty"]; ok && len(rawPresencePenalty) > 0 {
			_ = common.Unmarshal(rawPresencePenalty, &c.PresencePenalty)
		}
	}

	if _, ok := raw["frequencyPenalty"]; !ok {
		if rawFrequencyPenalty, ok := raw["frequency_penalty"]; ok && len(rawFrequencyPenalty) > 0 {
			_ = common.Unmarshal(rawFrequencyPenalty, &c.FrequencyPenalty)
		}
	}

	if _, ok := raw["responseLogprobs"]; !ok {
		if rawResponseLogprobs, ok := raw["response_logprobs"]; ok && len(rawResponseLogprobs) > 0 {
			_ = common.Unmarshal(rawResponseLogprobs, &c.ResponseLogprobs)
		}
	}

	if _, ok := raw["enableEnhancedCivicAnswers"]; !ok {
		if rawEnableEnhancedCivicAnswers, ok := raw["enable_enhanced_civic_answers"]; ok && len(rawEnableEnhancedCivicAnswers) > 0 {
			_ = common.Unmarshal(rawEnableEnhancedCivicAnswers, &c.EnableEnhancedCivicAnswers)
		}
	}

	if _, ok := raw["mediaResolution"]; !ok {
		if rawMediaResolution, ok := raw["media_resolution"]; ok && len(rawMediaResolution) > 0 {
			_ = common.Unmarshal(rawMediaResolution, &c.MediaResolution)
		}
	}

	if _, ok := raw["responseModalities"]; !ok {
		if rawResponseModalities, ok := raw["response_modalities"]; ok && len(rawResponseModalities) > 0 {
			_ = common.Unmarshal(rawResponseModalities, &c.ResponseModalities)
		}
	}

	if _, ok := raw["thinkingConfig"]; !ok {
		if rawThinkingConfig, ok := raw["thinking_config"]; ok && len(rawThinkingConfig) > 0 {
			var thinkingConfig GeminiThinkingConfig
			if err := common.Unmarshal(rawThinkingConfig, &thinkingConfig); err == nil {
				c.ThinkingConfig = &thinkingConfig
			}
		}
	}

	if _, ok := raw["speechConfig"]; !ok {
		if rawSpeechConfig, ok := raw["speech_config"]; ok && len(rawSpeechConfig) > 0 {
			c.SpeechConfig = rawSpeechConfig
		}
	}

	if _, ok := raw["imageConfig"]; !ok {
		if rawImageConfig, ok := raw["image_config"]; ok && len(rawImageConfig) > 0 {
			c.ImageConfig = rawImageConfig
		}
	}

	return nil
}

type MediaResolution string

type GeminiChatCandidate struct {
	Content       GeminiChatContent        `json:"content"`
	FinishReason  *string                  `json:"finishReason"`
	Index         int64                    `json:"index"`
	SafetyRatings []GeminiChatSafetyRating `json:"safetyRatings"`
}

type GeminiChatSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

type GeminiChatPromptFeedback struct {
	SafetyRatings []GeminiChatSafetyRating `json:"safetyRatings"`
	BlockReason   *string                  `json:"blockReason,omitempty"`
}

type GeminiChatResponse struct {
	Candidates     []GeminiChatCandidate     `json:"candidates"`
	PromptFeedback *GeminiChatPromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  GeminiUsageMetadata       `json:"usageMetadata"`
}

type GeminiUsageMetadata struct {
	PromptTokenCount           int                         `json:"promptTokenCount"`
	ToolUsePromptTokenCount    int                         `json:"toolUsePromptTokenCount"`
	CandidatesTokenCount       int                         `json:"candidatesTokenCount"`
	TotalTokenCount            int                         `json:"totalTokenCount"`
	ThoughtsTokenCount         int                         `json:"thoughtsTokenCount"`
	CachedContentTokenCount    int                         `json:"cachedContentTokenCount"`
	PromptTokensDetails        []GeminiPromptTokensDetails `json:"promptTokensDetails"`
	ToolUsePromptTokensDetails []GeminiPromptTokensDetails `json:"toolUsePromptTokensDetails"`
	CandidatesTokensDetails    []GeminiPromptTokensDetails `json:"candidatesTokensDetails"`
}

type GeminiPromptTokensDetails struct {
	Modality   string `json:"modality"`
	TokenCount int    `json:"tokenCount"`
}

// Imagen related structs
type GeminiImageRequest struct {
	Instances  []GeminiImageInstance `json:"instances"`
	Parameters GeminiImageParameters `json:"parameters"`
}

type GeminiImageInstance struct {
	Prompt string `json:"prompt"`
}

type GeminiImageParameters struct {
	SampleCount      int    `json:"sampleCount,omitempty"`
	AspectRatio      string `json:"aspectRatio,omitempty"`
	PersonGeneration string `json:"personGeneration,omitempty"`
	ImageSize        string `json:"imageSize,omitempty"`
}

type GeminiImageResponse struct {
	Predictions []GeminiImagePrediction `json:"predictions"`
}

type GeminiImagePrediction struct {
	MimeType           string `json:"mimeType"`
	BytesBase64Encoded string `json:"bytesBase64Encoded"`
	RaiFilteredReason  string `json:"raiFilteredReason,omitempty"`
	SafetyAttributes   any    `json:"safetyAttributes,omitempty"`
}

// Embedding related structs
type GeminiEmbeddingRequest struct {
	Model                string            `json:"model,omitempty"`
	Content              GeminiChatContent `json:"content"`
	TaskType             string            `json:"taskType,omitempty"`
	Title                string            `json:"title,omitempty"`
	OutputDimensionality int               `json:"outputDimensionality,omitempty"`
}

func (r *GeminiEmbeddingRequest) IsStream(c *gin.Context) bool {
	// Gemini embedding requests are not streamed
	return false
}

func (r *GeminiEmbeddingRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var inputTexts []string
	for _, part := range r.Content.Parts {
		if part.Text != "" {
			inputTexts = append(inputTexts, part.Text)
		}
	}
	inputText := strings.Join(inputTexts, "\n")
	return &types.TokenCountMeta{
		CombineText: inputText,
	}
}

func (r *GeminiEmbeddingRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

type GeminiBatchEmbeddingRequest struct {
	Requests []*GeminiEmbeddingRequest `json:"requests"`
}

func (r *GeminiBatchEmbeddingRequest) IsStream(c *gin.Context) bool {
	// Gemini batch embedding requests are not streamed
	return false
}

func (r *GeminiBatchEmbeddingRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var inputTexts []string
	for _, request := range r.Requests {
		meta := request.GetTokenCountMeta()
		if meta != nil && meta.CombineText != "" {
			inputTexts = append(inputTexts, meta.CombineText)
		}
	}
	inputText := strings.Join(inputTexts, "\n")
	return &types.TokenCountMeta{
		CombineText: inputText,
	}
}

func (r *GeminiBatchEmbeddingRequest) SetModelName(modelName string) {
	if modelName != "" {
		for _, req := range r.Requests {
			req.SetModelName(modelName)
		}
	}
}

type GeminiEmbeddingResponse struct {
	Embedding ContentEmbedding `json:"embedding"`
}

type GeminiBatchEmbeddingResponse struct {
	Embeddings []*ContentEmbedding `json:"embeddings"`
}

type ContentEmbedding struct {
	Values []float64 `json:"values"`
}
