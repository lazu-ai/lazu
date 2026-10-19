package openai_chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

var knownTopLevel = map[string]struct{}{
	"model": {}, "messages": {}, "stream": {}, "stream_options": {}, "max_tokens": {}, "max_completion_tokens": {},
	"reasoning_effort": {}, "temperature": {}, "top_p": {}, "top_k": {}, "stop": {}, "n": {},
	"response_format": {}, "seed": {}, "parallel_tool_calls": {}, "tools": {}, "tool_choice": {}, "user": {},
	"logprobs": {}, "top_logprobs": {}, "safety_identifier": {}, "store": {}, "prompt_cache_key": {},
	"metadata": {}, "web_search_options": {}, "service_tier": {},
	// OpenRouter-compatible gateway routing metadata is consumed before
	// protocol planning and must not make an otherwise lossless bridge fail.
	"models":  {},
	"OSS_URL": {}, "oss_url": {}, "OSS_URLS": {}, "oss_urls": {}, "ossUrl": {}, "ossUrls": {},
}

var ossURLHintFields = []string{"OSS_URL", "oss_url", "OSS_URLS", "oss_urls", "ossUrl", "ossUrls"}

func Decode(body []byte) (*textgen.Request, *dto.GeneralOpenAIRequest, error) {
	var wire dto.GeneralOpenAIRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(wire.Model) == "" {
		return nil, nil, fmt.Errorf("model is required")
	}
	if len(wire.Messages) == 0 && wire.Prefix == nil && wire.Suffix == nil {
		return nil, nil, fmt.Errorf("messages is required")
	}
	raw, err := textgen.RetainTopLevelFields(body)
	if err != nil {
		return nil, nil, err
	}
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: wire.Model}, Raw: textgen.RawRetention{Protocol: textgen.ProtocolOpenAIChat, TopLevel: raw, UnknownFields: unknownFields(raw)}}
	request.Lifecycle.Stream = wire.Stream
	if wire.StreamOptions != nil {
		request.Lifecycle.StreamIncludeUsage = textgen.Some(wire.StreamOptions.IncludeUsage)
	}
	markUnmodeledStreamOptions(raw, request)
	decodeOptions(raw, &wire, request)
	if err := decodeMessages(wire.Messages, request); err != nil {
		return nil, nil, err
	}
	if err := decodeTools(raw, &wire, request); err != nil {
		return nil, nil, err
	}
	if err := decodeOutput(wire.ResponseFormat, &request.Output); err != nil {
		return nil, nil, err
	}
	request.Reasoning.Effort = wire.ReasoningEffort
	request.Metadata.Metadata = append(json.RawMessage(nil), wire.Metadata...)
	request.Metadata.User = wire.User
	request.Metadata.SafetyIdentifier = wire.SafetyIdentifier
	request.Metadata.ServiceTier = wire.ServiceTier
	if wire.PromptCacheKey != "" {
		request.Metadata.PromptCacheKey, _ = json.Marshal(wire.PromptCacheKey)
	}
	return request, &wire, nil
}

// SyncEnrichedWire copies the message tree changed by controller-side gateway
// enrichments (currently OSS_URL media injection) into a cloned semantic
// request without decoding the JSON body a second time.
func SyncEnrichedWire(request *textgen.Request, wire *dto.GeneralOpenAIRequest) error {
	if request == nil || wire == nil {
		return fmt.Errorf("request is nil")
	}
	request.Conversation = nil
	request.Instructions = nil
	return decodeMessages(wire.Messages, request)
}

func HasOSSURLHint(request *textgen.Request) bool {
	if request == nil {
		return false
	}
	for _, key := range ossURLHintFields {
		if _, ok := request.Raw.TopLevel[key]; ok {
			return true
		}
	}
	return false
}

func decodeOptions(raw map[string]json.RawMessage, wire *dto.GeneralOpenAIRequest, request *textgen.Request) {
	if value, ok := raw["temperature"]; ok {
		var v float64
		if unmarshalNonNull(value, &v) {
			request.Sampling.Temperature = textgen.Some(v)
		}
	}
	if value, ok := raw["top_p"]; ok {
		var v float64
		if unmarshalNonNull(value, &v) {
			request.Sampling.TopP = textgen.Some(v)
		}
	}
	if value, ok := raw["top_k"]; ok {
		var v int
		if unmarshalNonNull(value, &v) {
			request.Sampling.TopK = textgen.Some(v)
		}
	}
	if value, ok := raw["seed"]; ok {
		var v int64
		if unmarshalNonNull(value, &v) {
			request.Sampling.Seed = textgen.Some(v)
		}
	}
	if value, ok := raw["stop"]; ok && !isJSONNull(value) {
		request.Sampling.Stop = textgen.Some(append(json.RawMessage(nil), value...))
	}
	if value, ok := raw["n"]; ok {
		var v int
		if unmarshalNonNull(value, &v) {
			request.Sampling.N = textgen.Some(v)
		}
	}
	if value, ok := raw["logprobs"]; ok {
		var v bool
		if unmarshalNonNull(value, &v) {
			request.Sampling.LogProbs = textgen.Some(v)
		}
	}
	if value, ok := raw["top_logprobs"]; ok {
		var v int
		if unmarshalNonNull(value, &v) {
			request.Sampling.TopLogProbs = textgen.Some(v)
		}
	}
	if value, ok := raw["max_completion_tokens"]; ok && !isJSONNull(value) {
		var v uint
		if unmarshalNonNull(value, &v) {
			request.Sampling.MaxOutput = textgen.Some(v)
		}
	} else if value, ok := raw["max_tokens"]; ok {
		var v uint
		if unmarshalNonNull(value, &v) {
			request.Sampling.MaxOutput = textgen.Some(v)
		}
	}
	if value, ok := raw["store"]; ok {
		var v bool
		if unmarshalNonNull(value, &v) {
			request.Lifecycle.Store = textgen.Some(v)
		}
	}
	_ = wire
}

func unmarshalNonNull[T any](value json.RawMessage, target *T) bool {
	return !isJSONNull(value) && json.Unmarshal(value, target) == nil
}

func isJSONNull(value json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(value)), "null")
}

func markUnmodeledStreamOptions(raw map[string]json.RawMessage, request *textgen.Request) {
	value, ok := raw["stream_options"]
	if !ok || isJSONNull(value) {
		return
	}
	var options map[string]json.RawMessage
	if json.Unmarshal(value, &options) != nil {
		return
	}
	for key, nested := range options {
		if key != "include_usage" && !isJSONNull(nested) {
			markUnknown(request, "stream_options."+key, nested)
		}
	}
}

func decodeMessages(messages []dto.Message, request *textgen.Request) error {
	seenConversation := false
	for index, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "" {
			role = "user"
		}
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			markUnknown(request, fmt.Sprintf("messages[%d].role", index), message.Role)
		}
		name := ""
		if message.Name != nil {
			name = strings.TrimSpace(*message.Name)
		}
		if name != "" {
			// Responses input messages have no participant-name equivalent.
			// Keep the original native wire, but make a cross-protocol plan fail
			// instead of silently changing attribution.
			markUnknown(request, fmt.Sprintf("messages[%d].name", index), name)
		}
		if role == "assistant" {
			if len(message.Audio) > 0 && !isJSONNull(message.Audio) {
				// Chat assistant audio history has no lossless Responses input
				// equivalent. Retain the native wire field, but fail a bridge closed.
				markUnknown(request, fmt.Sprintf("messages[%d].audio", index), message.Audio)
			}
			if message.Prefix != nil && *message.Prefix {
				// Responses input has no assistant-prefix continuation control.
				// Preserve native Chat handling while preventing a bridge from
				// silently changing prefix completion into an ordinary turn.
				markUnknown(request, fmt.Sprintf("messages[%d].prefix", index), true)
			}
			if message.Refusal != nil {
				// Responses input has no lossless equivalent for Chat's assistant
				// refusal history. Keep native Chat requests valid, but fail a
				// cross-protocol plan closed instead of dropping conversation state.
				markUnknown(request, fmt.Sprintf("messages[%d].refusal", index), *message.Refusal)
			}
			if strings.TrimSpace(message.ReasoningContent) != "" {
				markUnknown(request, fmt.Sprintf("messages[%d].reasoning_content", index), message.ReasoningContent)
			}
			if strings.TrimSpace(message.Reasoning) != "" {
				markUnknown(request, fmt.Sprintf("messages[%d].reasoning", index), message.Reasoning)
			}
			if len(message.Annotations) > 0 && !isJSONNull(message.Annotations) {
				var annotations []any
				if err := json.Unmarshal(message.Annotations, &annotations); err != nil || len(annotations) > 0 {
					markUnknown(request, fmt.Sprintf("messages[%d].annotations", index), message.Annotations)
				}
			}
		}
		if role == "system" || role == "developer" {
			if seenConversation {
				markUnknown(request, fmt.Sprintf("messages[%d].role", index), role)
			}
			if message.Content != nil && !message.IsStringContent() {
				// Responses instructions are a single string. Preserve the native
				// Chat wire, but fail a bridge closed before array part boundaries or
				// controls such as cache_control can be flattened by messageText.
				markUnknownMessageContent(message, index, request)
				markUnknown(request, fmt.Sprintf("messages[%d].content", index), message.Content)
			}
			request.Instructions = append(request.Instructions, textgen.Instruction{Role: role, Text: messageText(message)})
			continue
		}
		seenConversation = true
		if role == "tool" {
			if strings.TrimSpace(message.ToolCallId) == "" {
				return fmt.Errorf("messages[%d].tool_call_id is required", index)
			}
			if message.Content != nil && !message.IsStringContent() {
				// The semantic function-result representation is a string. Any
				// array-valued Chat tool result would otherwise be flattened and
				// become indistinguishable from the original structured content.
				markUnknown(request, fmt.Sprintf("messages[%d].content", index), message.Content)
			}
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionResult, Role: role, CallID: message.ToolCallId, Output: messageText(message)})
			continue
		}
		markUnknownMessageContent(message, index, request)
		parts, err := messageParts(message, index, request)
		if err != nil {
			return err
		}
		if len(parts) > 0 {
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: role, Name: name, Content: parts})
		}
		if role == "assistant" {
			for callIndex, call := range message.ParseToolCalls() {
				if hasToolCallExtraContent(call.ExtraContent) {
					// Provider-specific continuation metadata (for example a
					// Gemini thought signature) has no verified Responses input
					// equivalent. Keep native Chat history intact and fail a
					// cross-protocol plan closed instead of discarding it.
					markUnknown(request, fmt.Sprintf("messages[%d].tool_calls[%d].extra_content", index, callIndex), call.ExtraContent)
				}
				if !strings.EqualFold(call.Type, "function") {
					markUnknown(request, fmt.Sprintf("messages[%d].tool_calls[%d].type", index, callIndex), call.Type)
					continue
				}
				if call.ID == "" {
					return fmt.Errorf("messages[%d].tool_calls[%d].id is required", index, callIndex)
				}
				request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionCall, Role: role, CallID: call.ID, FunctionName: call.Function.Name, Arguments: call.Function.Arguments})
			}
		}
	}
	return nil
}

func hasToolCallExtraContent(value *dto.ToolCallExtraContent) bool {
	if value == nil {
		return false
	}
	raw, err := json.Marshal(value)
	return err != nil || string(raw) != "{}"
}

func markUnknownMessageContent(message dto.Message, index int, request *textgen.Request) {
	if message.Content == nil || message.IsStringContent() {
		return
	}
	items, ok := message.Content.([]any)
	if !ok {
		markUnknown(request, fmt.Sprintf("messages[%d].content", index), message.Content)
		return
	}
	for partIndex, item := range items {
		part, ok := item.(map[string]any)
		if !ok {
			markUnknown(request, fmt.Sprintf("messages[%d].content[%d]", index, partIndex), item)
			continue
		}
		kind, _ := part["type"].(string)
		if cacheControl, exists := part["cache_control"]; exists && cacheControl != nil {
			markUnknown(request, fmt.Sprintf("messages[%d].content[%d].cache_control", index, partIndex), cacheControl)
		}
		switch kind {
		case dto.ContentTypeText:
			markUnknownContentPartFields(request, index, partIndex, part, "type", "text", "cache_control")
		case dto.ContentTypeImageURL:
			markUnknownContentPartFields(request, index, partIndex, part, "type", "image_url", "cache_control")
			markUnknownNestedContentFields(request, index, partIndex, part, "image_url", "url", "detail")
		case dto.ContentTypeInputAudio:
			markUnknownContentPartFields(request, index, partIndex, part, "type", "input_audio", "cache_control")
			markUnknownNestedContentFields(request, index, partIndex, part, "input_audio", "data", "format")
		case dto.ContentTypeFile:
			markUnknownContentPartFields(request, index, partIndex, part, "type", "file", "cache_control")
			markUnknownNestedContentFields(request, index, partIndex, part, "file", "file_id", "file_data", "filename", "file_name")
		default:
			markUnknown(request, fmt.Sprintf("messages[%d].content[%d].type", index, partIndex), part)
		}
	}
}

func markUnknownContentPartFields(request *textgen.Request, messageIndex, partIndex int, part map[string]any, allowedKeys ...string) {
	for key, value := range part {
		if !containsString(allowedKeys, key) && value != nil {
			markUnknown(request, fmt.Sprintf("messages[%d].content[%d].%s", messageIndex, partIndex, key), value)
		}
	}
}

func markUnknownNestedContentFields(request *textgen.Request, messageIndex, partIndex int, part map[string]any, field string, allowedKeys ...string) {
	nested, ok := part[field].(map[string]any)
	if !ok {
		return
	}
	for key, value := range nested {
		if !containsString(allowedKeys, key) && value != nil {
			markUnknown(request, fmt.Sprintf("messages[%d].content[%d].%s.%s", messageIndex, partIndex, field, key), value)
		}
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func messageParts(message dto.Message, index int, request *textgen.Request) ([]textgen.ContentPart, error) {
	if message.Content == nil {
		return nil, nil
	}
	if message.IsStringContent() {
		return []textgen.ContentPart{{Kind: textgen.ContentText, Text: message.StringContent()}}, nil
	}
	parts := make([]textgen.ContentPart, 0)
	for partIndex, part := range message.ParseContent() {
		switch part.Type {
		case dto.ContentTypeText:
			parts = append(parts, textgen.ContentPart{Kind: textgen.ContentText, Text: part.Text})
		case dto.ContentTypeImageURL:
			image := part.GetImageMedia()
			if image != nil {
				parts = append(parts, textgen.ContentPart{Kind: textgen.ContentImage, URL: image.Url, Detail: explicitImageDetail(message.Content, partIndex)})
			}
		case dto.ContentTypeInputAudio:
			audio := part.GetInputAudio()
			if audio != nil {
				parts = append(parts, textgen.ContentPart{Kind: textgen.ContentAudio, Data: audio.Data, Format: audio.Format})
			}
		case dto.ContentTypeFile:
			file := part.GetFile()
			if file != nil {
				parts = append(parts, textgen.ContentPart{Kind: textgen.ContentFile, FileID: file.FileId, Data: file.FileData, Filename: file.FileName})
			}
		default:
			markUnknown(request, fmt.Sprintf("messages[%d].content[%d].type", index, partIndex), part)
		}
	}
	return parts, nil
}

func explicitImageDetail(content any, partIndex int) string {
	items, ok := content.([]any)
	if !ok || partIndex < 0 || partIndex >= len(items) {
		return ""
	}
	item, ok := items[partIndex].(map[string]any)
	if !ok {
		return ""
	}
	image, ok := item["image_url"].(map[string]any)
	if !ok {
		return ""
	}
	detail, _ := image["detail"].(string)
	return detail
}

func decodeTools(raw map[string]json.RawMessage, wire *dto.GeneralOpenAIRequest, request *textgen.Request) error {
	markUnknownToolFields(raw["tools"], request)
	for index, tool := range wire.Tools {
		if !strings.EqualFold(tool.Type, "function") {
			markUnknown(request, fmt.Sprintf("tools[%d].type", index), tool)
			continue
		}
		parameters, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			return err
		}
		definition := textgen.ToolDefinition{Type: "function", Name: tool.Function.Name, Description: tool.Function.Description, Parameters: parameters}
		if len(tool.Function.Strict) > 0 {
			var strict bool
			if err := json.Unmarshal(tool.Function.Strict, &strict); err != nil {
				return fmt.Errorf("tools[%d].function.strict must be boolean", index)
			}
			definition.Strict = textgen.Some(strict)
		}
		request.Tools = append(request.Tools, definition)
	}
	if wire.ParallelToolCalls != nil {
		request.ToolPolicy.ParallelToolCalls = textgen.Some(*wire.ParallelToolCalls)
	}
	switch choice := wire.ToolChoice.(type) {
	case string:
		request.ToolPolicy.Mode = choice
	case map[string]any:
		request.ToolPolicy.Mode = stringValue(choice["type"])
		if fn, ok := choice["function"].(map[string]any); ok {
			request.ToolPolicy.FunctionName = stringValue(fn["name"])
		}
	}
	if wire.WebSearchOptions != nil {
		raw, _ := json.Marshal(wire.WebSearchOptions)
		request.Tools = append(request.Tools, textgen.ToolDefinition{Type: dto.BuildInToolWebSearchPreview, Hosted: raw})
	}
	return nil
}

func markUnknownToolFields(raw json.RawMessage, request *textgen.Request) {
	if len(raw) == 0 || isJSONNull(raw) {
		return
	}
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return
	}
	for index, tool := range tools {
		markUnknownRawObjectFields(request, fmt.Sprintf("tools[%d]", index), tool, "type", "function")
		var function map[string]json.RawMessage
		if value, ok := tool["function"]; ok && !isJSONNull(value) && json.Unmarshal(value, &function) == nil {
			markUnknownRawObjectFields(request, fmt.Sprintf("tools[%d].function", index), function, "name", "description", "parameters", "strict")
		}
	}
}

func markUnknownRawObjectFields(request *textgen.Request, path string, object map[string]json.RawMessage, allowedKeys ...string) {
	for key, value := range object {
		if !containsString(allowedKeys, key) && !isJSONNull(value) {
			markUnknown(request, path+"."+key, value)
		}
	}
}

func markUnknown(request *textgen.Request, path string, value any) {
	if request == nil || strings.TrimSpace(path) == "" {
		return
	}
	if request.Raw.UnknownFields == nil {
		request.Raw.UnknownFields = make(map[string]json.RawMessage)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		raw = json.RawMessage(`null`)
	}
	request.Raw.UnknownFields[path] = raw
}

func decodeOutput(format *dto.ResponseFormat, output *textgen.OutputConstraint) error {
	if format == nil {
		return nil
	}
	output.Type = format.Type
	if len(format.JsonSchema) == 0 {
		return nil
	}
	var schema dto.FormatJsonSchema
	if err := json.Unmarshal(format.JsonSchema, &schema); err != nil {
		return err
	}
	output.Type = "json_schema"
	output.Name = schema.Name
	output.Description = schema.Description
	output.Schema, _ = json.Marshal(schema.Schema)
	if len(schema.Strict) > 0 {
		var strict bool
		if json.Unmarshal(schema.Strict, &strict) == nil {
			output.Strict = textgen.Some(strict)
		}
	}
	return nil
}

func Encode(request *textgen.Request) (*dto.GeneralOpenAIRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	wire := &dto.GeneralOpenAIRequest{Model: request.Model.Upstream, Stream: request.Lifecycle.Stream, ReasoningEffort: request.Reasoning.Effort, User: request.Metadata.User, SafetyIdentifier: request.Metadata.SafetyIdentifier, ServiceTier: request.Metadata.ServiceTier}
	if wire.Model == "" {
		wire.Model = request.Model.Canonical
	}
	if request.Lifecycle.StreamIncludeUsage.Set {
		wire.StreamOptions = &dto.StreamOptions{IncludeUsage: request.Lifecycle.StreamIncludeUsage.Value}
	}
	if request.Sampling.Temperature.Set {
		wire.Temperature = &request.Sampling.Temperature.Value
	}
	if request.Sampling.TopP.Set {
		wire.TopP = &request.Sampling.TopP.Value
	}
	if request.Sampling.TopK.Set {
		wire.TopK = request.Sampling.TopK.Value
	}
	if request.Sampling.Seed.Set {
		wire.Seed = float64(request.Sampling.Seed.Value)
	}
	if request.Sampling.Stop.Set {
		var stop any
		if err := json.Unmarshal(request.Sampling.Stop.Value, &stop); err != nil {
			return nil, err
		}
		wire.Stop = stop
	}
	if request.Sampling.N.Set {
		wire.N = request.Sampling.N.Value
	}
	if request.Sampling.LogProbs.Set {
		wire.LogProbs = request.Sampling.LogProbs.Value
	}
	if request.Sampling.TopLogProbs.Set {
		wire.TopLogProbs = request.Sampling.TopLogProbs.Value
	}
	if request.Sampling.MaxOutput.Set {
		wire.MaxCompletionTokens = request.Sampling.MaxOutput.Value
	}
	if request.ToolPolicy.ParallelToolCalls.Set {
		wire.ParallelToolCalls = &request.ToolPolicy.ParallelToolCalls.Value
	}
	wire.Metadata = append(json.RawMessage(nil), request.Metadata.Metadata...)
	if len(request.Metadata.PromptCacheKey) > 0 {
		var key string
		if json.Unmarshal(request.Metadata.PromptCacheKey, &key) == nil {
			wire.PromptCacheKey = key
		}
	}
	if err := encodeMessages(request, wire); err != nil {
		return nil, err
	}
	if err := encodeTools(request, wire); err != nil {
		return nil, err
	}
	if err := encodeOutput(request.Output, wire); err != nil {
		return nil, err
	}
	return wire, nil
}

func encodeMessages(request *textgen.Request, wire *dto.GeneralOpenAIRequest) error {
	for _, instruction := range request.Instructions {
		message := dto.Message{Role: instruction.Role}
		message.SetStringContent(instruction.Text)
		wire.Messages = append(wire.Messages, message)
	}
	pendingReasoning := -1
	for _, item := range request.Conversation {
		switch item.Kind {
		case textgen.InputReasoning:
			if !item.PlainReasoning.Set {
				return fmt.Errorf("opaque reasoning cannot be encoded as chat")
			}
			last := len(wire.Messages) - 1
			if last >= 0 && wire.Messages[last].Role == "assistant" && len(wire.Messages[last].ParseToolCalls()) > 0 && wire.Messages[last].ReasoningContent == "" {
				wire.Messages[last].ReasoningContent = item.PlainReasoning.Value
				pendingReasoning = -1
				if wire.Messages[last].Content == nil {
					pendingReasoning = last
				}
			} else {
				message := dto.Message{Role: "assistant", ReasoningContent: item.PlainReasoning.Value}
				message.SetNullContent()
				wire.Messages = append(wire.Messages, message)
				pendingReasoning = len(wire.Messages) - 1
			}
		case textgen.InputMessage:
			message := dto.Message{Role: item.Role}
			content, err := encodeContent(item.Content)
			if err != nil {
				return err
			}
			message.Content = content
			if item.Role == "assistant" && pendingReasoning >= 0 {
				wire.Messages[pendingReasoning].Content = content
			} else {
				wire.Messages = append(wire.Messages, message)
			}
			pendingReasoning = -1
		case textgen.InputFunctionCall:
			if len(wire.Messages) == 0 || wire.Messages[len(wire.Messages)-1].Role != "assistant" {
				m := dto.Message{Role: "assistant"}
				m.SetNullContent()
				wire.Messages = append(wire.Messages, m)
			}
			calls := wire.Messages[len(wire.Messages)-1].ParseToolCalls()
			calls = append(calls, dto.ToolCallRequest{ID: item.CallID, Type: "function", Function: dto.FunctionRequest{Name: item.FunctionName, Arguments: item.Arguments}})
			wire.Messages[len(wire.Messages)-1].SetToolCalls(calls)
		case textgen.InputFunctionResult:
			pendingReasoning = -1
			message := dto.Message{Role: "tool", ToolCallId: item.CallID}
			message.SetStringContent(item.Output)
			wire.Messages = append(wire.Messages, message)
		default:
			return fmt.Errorf("input kind %q cannot be encoded as chat", item.Kind)
		}
	}
	if len(wire.Messages) == 0 {
		return fmt.Errorf("messages is required")
	}
	return nil
}

func encodeContent(parts []textgen.ContentPart) (any, error) {
	if len(parts) == 1 && parts[0].Kind == textgen.ContentText {
		return parts[0].Text, nil
	}
	result := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case textgen.ContentText:
			result = append(result, map[string]any{"type": "text", "text": part.Text})
		case textgen.ContentImage:
			item := map[string]any{"url": part.URL}
			if part.Detail != "" {
				item["detail"] = part.Detail
			}
			result = append(result, map[string]any{"type": "image_url", "image_url": item})
		case textgen.ContentAudio:
			audio := map[string]any{"data": part.Data}
			if part.Format != "" {
				audio["format"] = part.Format
			}
			result = append(result, map[string]any{"type": "input_audio", "input_audio": audio})
		case textgen.ContentFile:
			file := map[string]any{}
			if part.FileID != "" {
				file["file_id"] = part.FileID
			} else {
				file["file_data"] = part.Data
				if part.Filename != "" {
					file["filename"] = part.Filename
				}
			}
			result = append(result, map[string]any{"type": "file", "file": file})
		default:
			return nil, fmt.Errorf("content kind %q cannot be encoded as chat", part.Kind)
		}
	}
	return result, nil
}

func encodeTools(request *textgen.Request, wire *dto.GeneralOpenAIRequest) error {
	for _, tool := range request.Tools {
		if tool.Type != "function" {
			return fmt.Errorf("hosted tool %q cannot be encoded as chat", tool.Type)
		}
		var parameters any
		if len(tool.Parameters) > 0 {
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				return err
			}
		}
		function := dto.FunctionRequest{Name: tool.Name, Description: tool.Description, Parameters: parameters}
		if tool.Strict.Set {
			function.Strict, _ = json.Marshal(tool.Strict.Value)
		}
		wire.Tools = append(wire.Tools, dto.ToolCallRequest{Type: "function", Function: function})
	}
	switch request.ToolPolicy.Mode {
	case "", "auto", "none", "required":
		if request.ToolPolicy.Mode != "" {
			wire.ToolChoice = request.ToolPolicy.Mode
		}
	case "function":
		wire.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": request.ToolPolicy.FunctionName}}
	default:
		return fmt.Errorf("tool choice %q cannot be encoded as chat", request.ToolPolicy.Mode)
	}
	return nil
}

func encodeOutput(output textgen.OutputConstraint, wire *dto.GeneralOpenAIRequest) error {
	if output.Type == "" {
		return nil
	}
	format := &dto.ResponseFormat{Type: output.Type}
	if output.Type == "json_schema" {
		var schema any
		if len(output.Schema) > 0 {
			if err := json.Unmarshal(output.Schema, &schema); err != nil {
				return err
			}
		}
		strict := json.RawMessage(nil)
		if output.Strict.Set {
			strict, _ = json.Marshal(output.Strict.Value)
		}
		raw, err := json.Marshal(dto.FormatJsonSchema{Name: output.Name, Description: output.Description, Schema: schema, Strict: strict})
		if err != nil {
			return err
		}
		format.JsonSchema = raw
	}
	wire.ResponseFormat = format
	return nil
}

func unknownFields(raw map[string]json.RawMessage) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for key, value := range raw {
		if _, ok := knownTopLevel[key]; !ok {
			result[key] = append(json.RawMessage(nil), value...)
		}
	}
	return result
}
func messageText(message dto.Message) string {
	if message.IsStringContent() {
		return message.StringContent()
	}
	var builder strings.Builder
	for _, part := range message.ParseContent() {
		if part.Type == dto.ContentTypeText {
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}
func stringValue(value any) string { text, _ := value.(string); return text }
