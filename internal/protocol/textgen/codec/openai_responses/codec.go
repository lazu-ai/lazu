package openai_responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	usagepkg "github.com/lazu-ai/lazu/internal/usage"
)

var knownTopLevel = map[string]struct{}{
	"model": {}, "input": {}, "include": {}, "conversation": {}, "instructions": {}, "max_output_tokens": {},
	"top_logprobs": {}, "metadata": {}, "parallel_tool_calls": {}, "previous_response_id": {}, "reasoning": {}, "service_tier": {},
	"store": {}, "prompt_cache_key": {}, "safety_identifier": {}, "stream": {}, "stream_options": {},
	"temperature": {}, "text": {}, "tool_choice": {}, "tools": {}, "top_p": {}, "user": {}, "prompt": {},
}

func Decode(body []byte) (*textgen.Request, *dto.OpenAIResponsesRequest, error) {
	var wire dto.OpenAIResponsesRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(wire.Model) == "" {
		return nil, nil, fmt.Errorf("model is required")
	}
	if len(wire.Input) == 0 {
		return nil, nil, fmt.Errorf("input is required")
	}
	raw, err := textgen.RetainTopLevelFields(body)
	if err != nil {
		return nil, nil, err
	}
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: wire.Model}, Raw: textgen.RawRetention{Protocol: textgen.ProtocolOpenAIResponses, TopLevel: raw, UnknownFields: unknownFields(raw)}}
	request.Lifecycle.Stream = wire.Stream
	if wire.StreamOptions != nil {
		request.Lifecycle.StreamIncludeUsage = textgen.Some(wire.StreamOptions.IncludeUsage)
	}
	markUnmodeledStreamOptions(raw, request)
	decodeOptions(raw, &wire, request)
	if err := decodeInstructions(wire.Instructions, request); err != nil {
		return nil, nil, err
	}
	if err := decodeInput(wire.Input, request); err != nil {
		return nil, nil, err
	}
	if err := decodeTools(&wire, request); err != nil {
		return nil, nil, err
	}
	if err := decodeOutput(wire.Text, request); err != nil {
		return nil, nil, err
	}
	if wire.Reasoning != nil {
		request.Reasoning.Effort = wire.Reasoning.Effort
		request.Reasoning.Summary = wire.Reasoning.Summary
	}
	request.Metadata.Metadata = append(json.RawMessage(nil), wire.Metadata...)
	request.Metadata.User = wire.User
	request.Metadata.SafetyIdentifier = wire.SafetyIdentifier
	request.Metadata.ServiceTier = wire.ServiceTier
	request.Metadata.PromptCacheKey = append(json.RawMessage(nil), wire.PromptCacheKey...)
	request.Lifecycle.PreviousResponseID = wire.PreviousResponseID
	request.Lifecycle.Conversation = append(json.RawMessage(nil), wire.Conversation...)
	request.Lifecycle.PromptTemplate = append(json.RawMessage(nil), wire.Prompt...)
	if len(wire.Include) > 0 {
		_ = json.Unmarshal(wire.Include, &request.Lifecycle.Include)
	}
	return request, &wire, nil
}

func decodeOptions(raw map[string]json.RawMessage, wire *dto.OpenAIResponsesRequest, request *textgen.Request) {
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
	if value, ok := raw["top_logprobs"]; ok {
		var v int
		if unmarshalNonNull(value, &v) {
			request.Sampling.TopLogProbs = textgen.Some(v)
			if v > 0 {
				request.Sampling.LogProbs = textgen.Some(true)
			}
		}
	}
	if value, ok := raw["max_output_tokens"]; ok {
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

func decodeInstructions(raw json.RawMessage, request *textgen.Request) error {
	if len(raw) == 0 {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		// Responses instructions have no role field. Use the portable system
		// instruction semantic so Chat-only providers are not required to
		// implement OpenAI's newer developer role.
		request.Instructions = append(request.Instructions, textgen.Instruction{Role: "system", Text: text})
		return nil
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return fmt.Errorf("instructions must be a string or content array")
	}
	for index, block := range blocks {
		for key, value := range block {
			if key != "type" && key != "text" && !isJSONNull(value) {
				markUnknown(request, fmt.Sprintf("instructions[%d].%s", index, key), value)
			}
		}
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		if kind != "" && kind != "input_text" && kind != "text" {
			markUnknown(request, fmt.Sprintf("instructions[%d].type", index), block["type"])
		}
		var value string
		if json.Unmarshal(block["text"], &value) != nil || value == "" {
			markUnknown(request, fmt.Sprintf("instructions[%d]", index), block)
			continue
		}
		request.Instructions = append(request.Instructions, textgen.Instruction{Role: "system", Text: value})
	}
	return nil
}

func decodeInput(raw json.RawMessage, request *textgen.Request) error {
	if items, ok := decodeSimpleTextInput(raw); ok {
		request.Conversation = append(request.Conversation, items...)
		return nil
	}

	var text string
	if !startsJSONArray(raw) && json.Unmarshal(raw, &text) == nil {
		request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: text}}})
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("input must be a string or item array")
	}
	for index, itemRaw := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			return fmt.Errorf("input[%d] must be an object", index)
		}
		var itemType string
		_ = json.Unmarshal(item["type"], &itemType)
		markUnknownInputItemFields(request, index, itemType, item)
		switch itemType {
		case "function_call":
			var call struct {
				CallID    string `json:"call_id"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Status    string `json:"status"`
			}
			if err := json.Unmarshal(itemRaw, &call); err != nil {
				return err
			}
			if call.CallID == "" {
				call.CallID = call.ID
			}
			if call.Status != "" && !strings.EqualFold(call.Status, "completed") {
				markUnknown(request, fmt.Sprintf("input[%d].status", index), call.Status)
			}
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionCall, Role: "assistant", CallID: call.CallID, FunctionName: call.Name, Arguments: call.Arguments})
		case "function_call_output":
			var result struct {
				CallID string `json:"call_id"`
				Output any    `json:"output"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(itemRaw, &result); err != nil {
				return err
			}
			if result.Status != "" && !strings.EqualFold(result.Status, "completed") {
				markUnknown(request, fmt.Sprintf("input[%d].status", index), result.Status)
			}
			output, stringOutput := result.Output.(string)
			if !stringOutput {
				// Chat tool messages cannot preserve Responses structured content
				// parts. Retain native decoding, but reject a lossy bridge.
				markUnknown(request, fmt.Sprintf("input[%d].output", index), result.Output)
			}
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionResult, Role: "tool", CallID: result.CallID, Output: output})
		case "reasoning":
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputReasoning, PlainReasoning: decodeGatewayReasoning(item), Opaque: []textgen.OpaqueValue{{Dialect: "openai", Value: append(json.RawMessage(nil), itemRaw...)}}})
		case "item_reference":
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputReference, Opaque: []textgen.OpaqueValue{{Dialect: "openai", Value: append(json.RawMessage(nil), itemRaw...)}}})
		default:
			if itemType != "" && itemType != "message" {
				markUnknown(request, fmt.Sprintf("input[%d].type", index), itemRaw)
				request.Conversation = append(request.Conversation, opaqueInputItem(itemType, itemRaw))
				continue
			}
			var status string
			_ = json.Unmarshal(item["status"], &status)
			if status != "" && !strings.EqualFold(status, "completed") {
				markUnknown(request, fmt.Sprintf("input[%d].status", index), status)
			}
			var role string
			_ = json.Unmarshal(item["role"], &role)
			if role == "" {
				role = "user"
			}
			switch role {
			case "system", "developer", "user", "assistant", "tool":
			default:
				markUnknown(request, fmt.Sprintf("input[%d].role", index), role)
			}
			parts, err := decodeContent(item["content"], index, request)
			if err != nil {
				markUnknown(request, fmt.Sprintf("input[%d].content", index), itemRaw)
				request.Conversation = append(request.Conversation, opaqueInputItem("message", itemRaw))
				continue
			}
			request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: role, Content: parts})
		}
	}
	if len(request.Conversation) == 0 {
		return fmt.Errorf("input is empty")
	}
	return nil
}

// Unmarshal validates the complete payload before reporting an array/string
// type mismatch. Skip that wasted scan only for an actual JSON array token;
// nulls, strings and malformed non-array values keep their original behavior.
func startsJSONArray(raw []byte) bool {
	for _, char := range raw {
		switch char {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return char == '['
		}
	}
	return false
}

var inputItemAllowedKeys = map[string]map[string]struct{}{
	// Item ids are inert without server-side state (item_reference /
	// store=true are planned separately), so a Chat bridge may drop them.
	"message":              {"type": {}, "role": {}, "content": {}, "status": {}, "id": {}},
	"function_call":        {"type": {}, "call_id": {}, "id": {}, "name": {}, "arguments": {}, "status": {}},
	"function_call_output": {"type": {}, "call_id": {}, "output": {}, "status": {}},
}

func markUnknownInputItemFields(request *textgen.Request, itemIndex int, kind string, item map[string]json.RawMessage) {
	if kind == "" {
		kind = "message"
	}
	allowed, recognized := inputItemAllowedKeys[kind]
	if !recognized {
		return
	}
	for key, value := range item {
		// Older gateway output serialized these unrelated fields as empty
		// strings. They carry no state; meaningful values remain native-only.
		if (kind == "message" || kind == "function_call") && string(value) == `""` && (key == "quality" || key == "size" || (kind == "function_call" && key == "role")) {
			continue
		}
		if _, ok := allowed[key]; !ok && !isJSONNull(value) {
			markUnknown(request, fmt.Sprintf("input[%d].%s", itemIndex, key), value)
		}
	}
}

// opaqueInputItem retains an unmodeled input item verbatim for native
// Responses relays. It is deliberately distinct from item_reference: a
// reference needs server-side state, an opaque item merely needs the native
// wire format.
func opaqueInputItem(wireType string, raw json.RawMessage) textgen.InputItem {
	return textgen.InputItem{Kind: textgen.InputOpaque, WireType: wireType, Opaque: []textgen.OpaqueValue{{Dialect: "openai", Value: append(json.RawMessage(nil), raw...)}}}
}

func markUnknown(request *textgen.Request, path string, value any) {
	if request == nil || strings.TrimSpace(path) == "" {
		return
	}
	if request.Raw.UnknownFields == nil {
		request.Raw.UnknownFields = make(map[string]json.RawMessage)
	}
	var raw json.RawMessage
	switch typed := value.(type) {
	case json.RawMessage:
		raw = append(json.RawMessage(nil), typed...)
	case []byte:
		raw = append(json.RawMessage(nil), typed...)
	default:
		raw, _ = json.Marshal(value)
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	request.Raw.UnknownFields[path] = raw
}

var contentPartAllowedKeys = map[string]map[string]struct{}{
	"input_text":  {"type": {}, "text": {}},
	"output_text": {"type": {}, "text": {}, "annotations": {}},
	"text":        {"type": {}, "text": {}},
	"input_image": {"type": {}, "image_url": {}, "file_id": {}, "detail": {}},
	"input_file":  {"type": {}, "file_url": {}, "file_data": {}, "file_id": {}, "filename": {}},
	"input_audio": {"type": {}, "data": {}, "format": {}},
}

func markUnknownContentPartFields(request *textgen.Request, itemIndex, partIndex int, kind string, part map[string]json.RawMessage) {
	allowed, recognized := contentPartAllowedKeys[kind]
	if !recognized {
		return
	}
	for key, value := range part {
		if _, ok := allowed[key]; ok || isJSONNull(value) {
			continue
		}
		markUnknown(request, fmt.Sprintf("input[%d].content[%d].%s", itemIndex, partIndex, key), value)
	}
}

func decodeContent(raw json.RawMessage, itemIndex int, request *textgen.Request) ([]textgen.ContentPart, error) {
	var text string
	if !startsJSONArray(raw) && json.Unmarshal(raw, &text) == nil {
		return []textgen.ContentPart{{Kind: textgen.ContentText, Text: text}}, nil
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("input[%d].content must be a string or array", itemIndex)
	}
	result := make([]textgen.ContentPart, 0, len(parts))
	for partIndex, part := range parts {
		var kind string
		_ = json.Unmarshal(part["type"], &kind)
		markUnknownContentPartFields(request, itemIndex, partIndex, kind, part)
		if kind == "output_text" {
			if annotations, exists := part["annotations"]; exists && !isJSONNull(annotations) {
				var values []any
				if err := json.Unmarshal(annotations, &values); err != nil || len(values) > 0 {
					markUnknown(request, fmt.Sprintf("input[%d].content[%d].annotations", itemIndex, partIndex), annotations)
				}
			}
		}
		switch kind {
		case "input_text", "output_text", "text":
			var value string
			_ = json.Unmarshal(part["text"], &value)
			result = append(result, textgen.ContentPart{Kind: textgen.ContentText, Text: value})
		case "input_image":
			var url, detail, fileID string
			_ = json.Unmarshal(part["image_url"], &url)
			_ = json.Unmarshal(part["detail"], &detail)
			_ = json.Unmarshal(part["file_id"], &fileID)
			result = append(result, textgen.ContentPart{Kind: textgen.ContentImage, URL: url, FileID: fileID, Detail: detail})
		case "input_file":
			var url, data, id, name string
			_ = json.Unmarshal(part["file_url"], &url)
			_ = json.Unmarshal(part["file_data"], &data)
			_ = json.Unmarshal(part["file_id"], &id)
			_ = json.Unmarshal(part["filename"], &name)
			result = append(result, textgen.ContentPart{Kind: textgen.ContentFile, URL: url, Data: data, FileID: id, Filename: name})
		case "input_audio":
			var data, format string
			_ = json.Unmarshal(part["data"], &data)
			_ = json.Unmarshal(part["format"], &format)
			result = append(result, textgen.ContentPart{Kind: textgen.ContentAudio, Data: data, Format: format})
		default:
			return nil, fmt.Errorf("input[%d].content[%d].type %q is unsupported", itemIndex, partIndex, kind)
		}
	}
	return result, nil
}

func decodeTools(wire *dto.OpenAIResponsesRequest, request *textgen.Request) error {
	var tools []map[string]json.RawMessage
	if len(wire.Tools) > 0 {
		if err := json.Unmarshal(wire.Tools, &tools); err != nil {
			return err
		}
	}
	for index, tool := range tools {
		var kind string
		_ = json.Unmarshal(tool["type"], &kind)
		if kind == "function" {
			for key, value := range tool {
				switch key {
				case "type", "name", "description", "parameters", "strict":
				default:
					markUnknown(request, fmt.Sprintf("tools[%d].%s", index, key), value)
				}
			}
			var name, description string
			_ = json.Unmarshal(tool["name"], &name)
			_ = json.Unmarshal(tool["description"], &description)
			if name == "" {
				return fmt.Errorf("tools[%d].name is required", index)
			}
			definition := textgen.ToolDefinition{Type: "function", Name: name, Description: description, Parameters: append(json.RawMessage(nil), tool["parameters"]...)}
			if rawStrict, ok := tool["strict"]; ok {
				var strict bool
				if err := json.Unmarshal(rawStrict, &strict); err != nil {
					return fmt.Errorf("tools[%d].strict must be boolean", index)
				}
				definition.Strict = textgen.Some(strict)
			}
			request.Tools = append(request.Tools, definition)
		} else {
			raw, _ := json.Marshal(tool)
			request.Tools = append(request.Tools, textgen.ToolDefinition{Type: kind, Hosted: raw})
		}
	}
	if len(wire.ParallelToolCalls) > 0 {
		var value bool
		if json.Unmarshal(wire.ParallelToolCalls, &value) == nil {
			request.ToolPolicy.ParallelToolCalls = textgen.Some(value)
		}
	}
	if len(wire.ToolChoice) > 0 {
		var mode string
		if json.Unmarshal(wire.ToolChoice, &mode) == nil {
			request.ToolPolicy.Mode = mode
			if mode != "" && mode != "auto" && mode != "none" && mode != "required" {
				markUnknown(request, "tool_choice", wire.ToolChoice)
			}
		} else {
			var choice map[string]json.RawMessage
			if json.Unmarshal(wire.ToolChoice, &choice) == nil {
				_ = json.Unmarshal(choice["type"], &request.ToolPolicy.Mode)
				_ = json.Unmarshal(choice["name"], &request.ToolPolicy.FunctionName)
				if request.ToolPolicy.Mode != "function" {
					markUnknown(request, "tool_choice", wire.ToolChoice)
				} else {
					for key, value := range choice {
						if key != "type" && key != "name" {
							markUnknown(request, "tool_choice."+key, value)
						}
					}
				}
			}
		}
	}
	return nil
}

func decodeOutput(raw json.RawMessage, request *textgen.Request) error {
	if len(raw) == 0 {
		return nil
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return err
	}
	for key, value := range wrapper {
		if key != "format" {
			// text.verbosity and future text controls have no verified Chat
			// equivalent. Native Responses remains pass-through; bridges fail closed.
			markUnknown(request, "text."+key, value)
		}
	}
	var format map[string]json.RawMessage
	if err := json.Unmarshal(wrapper["format"], &format); err != nil && len(wrapper["format"]) > 0 {
		return err
	}
	if format == nil {
		return nil
	}
	output := &request.Output
	_ = json.Unmarshal(format["type"], &output.Type)
	_ = json.Unmarshal(format["name"], &output.Name)
	_ = json.Unmarshal(format["description"], &output.Description)
	output.Schema = append(json.RawMessage(nil), format["schema"]...)
	if value, ok := format["strict"]; ok {
		var strict bool
		if json.Unmarshal(value, &strict) == nil {
			output.Strict = textgen.Some(strict)
		}
	}
	return nil
}

func Encode(request *textgen.Request) (*dto.OpenAIResponsesRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	wire := &dto.OpenAIResponsesRequest{Model: request.Model.Upstream, Stream: request.Lifecycle.Stream, User: request.Metadata.User, SafetyIdentifier: request.Metadata.SafetyIdentifier, ServiceTier: request.Metadata.ServiceTier}
	if wire.Model == "" {
		wire.Model = request.Model.Canonical
	}
	if err := encodeResponseConfiguration(request, wire); err != nil {
		return nil, err
	}
	if err := encodeInput(request, wire); err != nil {
		return nil, err
	}
	if err := encodeOutput(request.Output, wire); err != nil {
		return nil, err
	}
	return wire, nil
}

// encodeResponseConfiguration is shared by request encoding and bridged
// response envelopes so that the latter describe the effective semantic
// request instead of serializing zero-value placeholders.
func encodeResponseConfiguration(request *textgen.Request, wire *dto.OpenAIResponsesRequest) error {
	if request == nil || wire == nil {
		return nil
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
	if request.Sampling.TopLogProbs.Set {
		wire.TopLogProbs = &request.Sampling.TopLogProbs.Value
	}
	if request.Sampling.MaxOutput.Set {
		wire.MaxOutputTokens = request.Sampling.MaxOutput.Value
	}
	if request.ToolPolicy.ParallelToolCalls.Set {
		wire.ParallelToolCalls, _ = json.Marshal(request.ToolPolicy.ParallelToolCalls.Value)
	}
	// Responses takes an effort and no budget: a budget is sent as the
	// effort it reaches (textgen.EffortForBudget)
	if effort := textgen.ReasoningEffortFor(request.Reasoning); effort != "" || request.Reasoning.Summary != "" {
		wire.Reasoning = &dto.Reasoning{Effort: effort, Summary: request.Reasoning.Summary}
	}
	wire.Metadata = append(json.RawMessage(nil), request.Metadata.Metadata...)
	wire.PromptCacheKey = append(json.RawMessage(nil), request.Metadata.PromptCacheKey...)
	if request.Lifecycle.Store.Set {
		wire.Store, _ = json.Marshal(request.Lifecycle.Store.Value)
	} else if request.Raw.Protocol == textgen.ProtocolOpenAIChat {
		// Chat Completions defaults to not storing requests, while Responses
		// defaults to storing them. Preserve the client protocol's effective
		// behavior when an omitted Chat field crosses this protocol boundary.
		wire.Store = json.RawMessage(`false`)
	}
	if len(request.Lifecycle.Include) > 0 {
		wire.Include, _ = json.Marshal(request.Lifecycle.Include)
	}
	wire.PreviousResponseID = request.Lifecycle.PreviousResponseID
	wire.Conversation = append(json.RawMessage(nil), request.Lifecycle.Conversation...)
	wire.Prompt = append(json.RawMessage(nil), request.Lifecycle.PromptTemplate...)
	if err := encodeInstructions(request, wire); err != nil {
		return err
	}
	return encodeTools(request, wire)
}

func encodeInstructions(request *textgen.Request, wire *dto.OpenAIResponsesRequest) error {
	if len(request.Instructions) == 0 {
		return nil
	}
	texts := make([]string, 0, len(request.Instructions))
	for _, instruction := range request.Instructions {
		if strings.TrimSpace(instruction.Text) != "" {
			texts = append(texts, instruction.Text)
		}
	}
	if len(texts) > 0 {
		wire.Instructions, _ = json.Marshal(strings.Join(texts, "\n\n"))
	}
	return nil
}
func encodeInput(request *textgen.Request, wire *dto.OpenAIResponsesRequest) error {
	items := make([]map[string]any, 0, len(request.Conversation))
	for _, item := range request.Conversation {
		switch item.Kind {
		case textgen.InputMessage:
			content, err := encodeContent(item)
			if err != nil {
				return err
			}
			items = append(items, map[string]any{"role": item.Role, "content": content})
		case textgen.InputFunctionCall:
			items = append(items, map[string]any{"type": "function_call", "call_id": item.CallID, "name": item.FunctionName, "arguments": item.Arguments})
		case textgen.InputFunctionResult:
			items = append(items, map[string]any{"type": "function_call_output", "call_id": item.CallID, "output": functionOutput(item)})
		case textgen.InputReasoning, textgen.InputReference, textgen.InputOpaque:
			if item.Kind == textgen.InputReasoning && len(item.Opaque) > 0 && item.Opaque[0].Dialect == "anthropic" {
				// sealed by Anthropic, meaningless to another vendor
				continue
			}
			if len(item.Opaque) == 0 || item.Opaque[0].Dialect != "openai" {
				return fmt.Errorf("opaque %s item cannot cross provider dialect", item.Kind)
			}
			var value map[string]any
			if err := json.Unmarshal(item.Opaque[0].Value, &value); err != nil {
				return err
			}
			items = append(items, value)
		default:
			return fmt.Errorf("input kind %q cannot be encoded as responses", item.Kind)
		}
	}
	if len(items) == 0 {
		return fmt.Errorf("input is required")
	}
	wire.Input, _ = json.Marshal(items)
	return nil
}

// functionOutput is a function result's output: its text, or, when the tool
// returned images too, the content list Responses takes for one.
func functionOutput(item textgen.InputItem) any {
	text := textgen.ResultText(item)
	var images []map[string]any
	for _, part := range item.Content {
		if part.Kind == textgen.ContentImage {
			image := map[string]any{"type": "input_image", "image_url": part.URL}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			images = append(images, image)
		}
	}
	if len(images) == 0 {
		return text
	}
	out := make([]map[string]any, 0, len(images)+1)
	if text != "" {
		out = append(out, map[string]any{"type": "input_text", "text": text})
	}
	return append(out, images...)
}

func encodeContent(item textgen.InputItem) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(item.Content))
	for _, part := range item.Content {
		switch part.Kind {
		case textgen.ContentText:
			kind := "input_text"
			if item.Role == "assistant" {
				kind = "output_text"
			}
			result = append(result, map[string]any{"type": kind, "text": part.Text})
		case textgen.ContentImage:
			value := map[string]any{"type": "input_image", "image_url": part.URL}
			if part.Detail != "" {
				value["detail"] = part.Detail
			}
			result = append(result, value)
		case textgen.ContentFile:
			value := map[string]any{"type": "input_file"}
			if part.FileID != "" {
				value["file_id"] = part.FileID
			} else if part.URL != "" {
				value["file_url"] = part.URL
			} else {
				value["file_data"] = part.Data
				if part.Filename != "" {
					value["filename"] = part.Filename
				}
			}
			result = append(result, value)
		case textgen.ContentAudio:
			return nil, fmt.Errorf("audio input has no verified Responses bridge mapping")
		default:
			return nil, fmt.Errorf("content kind %q cannot be encoded as responses", part.Kind)
		}
	}
	return result, nil
}
func encodeTools(request *textgen.Request, wire *dto.OpenAIResponsesRequest) error {
	tools := make([]map[string]any, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if tool.Type != "function" {
			return fmt.Errorf("hosted tool %q is not lossless across protocols", tool.Type)
		}
		item := map[string]any{"type": "function", "name": tool.Name}
		if tool.Description != "" {
			item["description"] = tool.Description
		}
		if len(tool.Parameters) > 0 {
			var parameters any
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				return err
			}
			item["parameters"] = parameters
		}
		if tool.Strict.Set {
			item["strict"] = tool.Strict.Value
		}
		tools = append(tools, item)
	}
	if len(tools) > 0 {
		wire.Tools, _ = json.Marshal(tools)
	}
	switch request.ToolPolicy.Mode {
	case "":
	case "auto", "none", "required":
		wire.ToolChoice, _ = json.Marshal(request.ToolPolicy.Mode)
	case "function":
		wire.ToolChoice, _ = json.Marshal(map[string]any{"type": "function", "name": request.ToolPolicy.FunctionName})
	default:
		return fmt.Errorf("tool choice %q cannot be encoded as responses", request.ToolPolicy.Mode)
	}
	return nil
}
func encodeOutput(output textgen.OutputConstraint, wire *dto.OpenAIResponsesRequest) error {
	if output.Type == "" {
		return nil
	}
	format := map[string]any{"type": output.Type}
	if output.Type == "json_schema" {
		format["name"] = output.Name
		if output.Description != "" {
			format["description"] = output.Description
		}
		if len(output.Schema) > 0 {
			var schema any
			if err := json.Unmarshal(output.Schema, &schema); err != nil {
				return err
			}
			format["schema"] = schema
		}
		if output.Strict.Set {
			format["strict"] = output.Strict.Value
		}
	}
	wire.Text, _ = json.Marshal(map[string]any{"format": format})
	return nil
}

func ChatResponseToResponses(response *dto.OpenAITextResponse, fallbackModel string, semanticRequest ...*textgen.Request) *dto.OpenAIResponsesResponse {
	if response == nil {
		response = &dto.OpenAITextResponse{}
	}
	created := int(time.Now().Unix())
	switch value := response.Created.(type) {
	case int:
		created = value
	case int64:
		created = int(value)
	case float64:
		created = int(value)
	}
	model := response.Model
	if model == "" {
		model = fallbackModel
	}
	result := &dto.OpenAIResponsesResponse{ID: normalizeResponseID(response.Id), Object: "response", CreatedAt: created, Status: "completed", Model: model, Output: []dto.ResponsesOutput{}, Usage: chatUsageToResponses(&response.Usage)}
	var request *textgen.Request
	if len(semanticRequest) > 0 {
		request = semanticRequest[0]
	}
	applyDefaultResponseConfiguration(result, request)
	applyResponseConfiguration(result, request)
	for choiceIndex, choice := range response.Choices {
		messageID := fmt.Sprintf("msg_%s_%d", stableSuffix(response.Id), choiceIndex)
		if reasoning := choice.Message.ReasoningContent; strings.TrimSpace(reasoning) != "" {
			result.Output = append(result.Output, dto.ResponsesOutput{Type: "reasoning", ID: fmt.Sprintf("rs_lazu_%s_%d", stableSuffix(response.Id), choiceIndex), Status: "completed", Content: []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: reasoning}}})
		} else if reasoning := choice.Message.Reasoning; strings.TrimSpace(reasoning) != "" {
			result.Output = append(result.Output, dto.ResponsesOutput{Type: "reasoning", ID: fmt.Sprintf("rs_lazu_%s_%d", stableSuffix(response.Id), choiceIndex), Status: "completed", Content: []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: reasoning}}})
		}
		text := choice.Message.StringContent()
		contentParts := make([]dto.ResponsesOutputContent, 0, 2)
		if (choice.Message.Content != nil && choice.Message.IsStringContent()) || len(choice.Message.Annotations) > 0 {
			content := dto.ResponsesOutputContent{Type: "output_text", Text: text}
			if len(choice.Message.Annotations) > 0 {
				_ = json.Unmarshal(choice.Message.Annotations, &content.Annotations)
			}
			contentParts = append(contentParts, content)
		}
		if choice.Message.Refusal != nil {
			contentParts = append(contentParts, dto.ResponsesOutputContent{Type: "refusal", Refusal: *choice.Message.Refusal})
		}
		if len(contentParts) > 0 {
			result.Output = append(result.Output, dto.ResponsesOutput{Type: "message", ID: messageID, Status: "completed", Role: "assistant", Content: contentParts})
		}
		for callIndex, call := range choice.Message.ParseToolCalls() {
			callID := call.ID
			if callID == "" {
				callID = fmt.Sprintf("call_%s_%d_%d", stableSuffix(response.Id), choiceIndex, callIndex)
			}
			result.Output = append(result.Output, dto.ResponsesOutput{Type: dto.ResponsesOutputTypeFunctionCall, ID: fmt.Sprintf("fc_%s_%d_%d", stableSuffix(response.Id), choiceIndex, callIndex), Status: "completed", CallID: callID, Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
		switch strings.ToLower(strings.TrimSpace(choice.FinishReason)) {
		case "length":
			result.Status = "incomplete"
			result.IncompleteDetails = &dto.IncompleteDetails{Reason: "max_output_tokens"}
		case "content_filter":
			result.Status = "incomplete"
			result.IncompleteDetails = &dto.IncompleteDetails{Reason: "content_filter"}
		}
	}
	if result.Status == "incomplete" {
		for index := range result.Output {
			result.Output[index].Status = "incomplete"
		}
	}
	return result
}

func defaultResponseToolChoice(request *textgen.Request) string {
	if request != nil && len(request.Tools) > 0 {
		return "auto"
	}
	return "none"
}

func applyDefaultResponseConfiguration(response *dto.OpenAIResponsesResponse, request *textgen.Request) {
	if response == nil {
		return
	}
	response.ParallelToolCalls = true
	response.Temperature = 1
	response.TopP = 1
	response.Truncation = "disabled"
	response.Tools = make([]map[string]any, 0)
	response.ToolChoice, _ = json.Marshal(defaultResponseToolChoice(request))
}

func applyResponseConfiguration(response *dto.OpenAIResponsesResponse, request *textgen.Request) {
	if response == nil || request == nil {
		return
	}
	wire := &dto.OpenAIResponsesRequest{}
	if err := encodeResponseConfiguration(request, wire); err != nil {
		return
	}
	response.Instructions = append(json.RawMessage(nil), wire.Instructions...)
	if request.Sampling.MaxOutput.Set {
		maxOutputTokens := int(wire.MaxOutputTokens)
		response.MaxOutputTokens = &maxOutputTokens
	}
	if len(wire.ParallelToolCalls) > 0 {
		_ = json.Unmarshal(wire.ParallelToolCalls, &response.ParallelToolCalls)
	}
	response.PreviousResponseID = wire.PreviousResponseID
	response.Reasoning = wire.Reasoning
	if len(wire.Store) > 0 {
		_ = json.Unmarshal(wire.Store, &response.Store)
	}
	if wire.Temperature != nil {
		response.Temperature = *wire.Temperature
	}
	if len(wire.ToolChoice) > 0 {
		response.ToolChoice = append(json.RawMessage(nil), wire.ToolChoice...)
	}
	if len(wire.Tools) > 0 {
		_ = json.Unmarshal(wire.Tools, &response.Tools)
	}
	if wire.TopP != nil {
		response.TopP = *wire.TopP
	}
	response.Metadata = append(json.RawMessage(nil), wire.Metadata...)
	if request.Metadata.User != "" {
		response.User, _ = json.Marshal(request.Metadata.User)
	}
}

func ResponsesResponseToChat(response *dto.OpenAIResponsesResponse, fallbackModel string) *dto.OpenAITextResponse {
	if response == nil {
		response = &dto.OpenAIResponsesResponse{}
	}
	created := int64(response.CreatedAt)
	if created <= 0 {
		created = time.Now().Unix()
	}
	model := response.Model
	if model == "" {
		model = fallbackModel
	}
	message := dto.Message{Role: "assistant"}
	var builder strings.Builder
	var refusal strings.Builder
	annotations := make([]interface{}, 0)
	calls := make([]dto.ToolCallResponse, 0)
	incomplete := strings.EqualFold(response.Status, "incomplete")
	preservePartialCalls := incomplete && response.IncompleteDetails != nil && strings.EqualFold(response.IncompleteDetails.Reason, "max_output_tokens")
	for _, output := range response.Output {
		if output.Type == dto.ResponsesOutputTypeFunctionCall {
			if incomplete && !preservePartialCalls && !strings.EqualFold(output.Status, "completed") {
				continue
			}
			callID := output.CallID
			if callID == "" {
				callID = output.ID
			}
			calls = append(calls, dto.ToolCallResponse{ID: callID, Type: "function", Function: dto.FunctionResponse{Name: output.Name, Arguments: output.Arguments}})
			continue
		}
		for _, content := range output.Content {
			switch content.Type {
			case "text", "output_text":
				builder.WriteString(content.Text)
				annotations = append(annotations, content.Annotations...)
			case "refusal":
				refusal.WriteString(content.Refusal)
			}
		}
	}
	if builder.Len() > 0 {
		message.SetStringContent(builder.String())
	} else if len(calls) > 0 || refusal.Len() > 0 {
		message.SetNullContent()
	} else {
		message.SetStringContent("")
	}
	if len(calls) > 0 {
		message.SetToolCalls(calls)
	}
	if refusal.Len() > 0 {
		value := refusal.String()
		message.Refusal = &value
	}
	if len(annotations) > 0 {
		message.Annotations, _ = json.Marshal(annotations)
	}
	finish := "stop"
	if incomplete {
		finish = "length"
		if response.IncompleteDetails != nil && strings.EqualFold(response.IncompleteDetails.Reason, "content_filter") {
			finish = "content_filter"
		}
	} else if len(calls) > 0 {
		finish = "tool_calls"
	}
	usage := responsesUsageToChat(response.Usage)
	return &dto.OpenAITextResponse{Id: NormalizeChatCompletionID(response.ID), Object: "chat.completion", Created: created, Model: model, Choices: []dto.OpenAITextResponseChoice{{Index: 0, Message: message, FinishReason: finish}}, Usage: usage}
}

func chatUsageToResponses(usage *dto.Usage) *dto.Usage {
	if usage == nil {
		return nil
	}
	copyUsage := *usage
	usagepkg.NormalizeDTOUsageFields(&copyUsage)
	copyUsage.InputTokens = copyUsage.PromptTokens
	copyUsage.OutputTokens = copyUsage.CompletionTokens
	input := copyUsage.PromptTokensDetails
	output := copyUsage.CompletionTokenDetails
	copyUsage.InputTokensDetails = &input
	copyUsage.OutputTokensDetails = &output
	return &copyUsage
}
func responsesUsageToChat(usage *dto.Usage) dto.Usage {
	if usage == nil {
		return dto.Usage{}
	}
	copyUsage := *usage
	usagepkg.NormalizeDTOUsageFields(&copyUsage)
	if copyUsage.PromptTokens == 0 {
		copyUsage.PromptTokens = copyUsage.InputTokens
	}
	if copyUsage.CompletionTokens == 0 {
		copyUsage.CompletionTokens = copyUsage.OutputTokens
	}
	if copyUsage.TotalTokens == 0 {
		copyUsage.TotalTokens = copyUsage.PromptTokens + copyUsage.CompletionTokens
	}
	return *dto.ProjectChatUsage(&copyUsage)
}
func stableSuffix(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "chatcmpl-")
	if value == "" {
		return "lazu"
	}
	return value
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

// decodeSimpleTextInput avoids repeated raw-message copies for common text
// message arrays. Only exact keys and string values qualify; all extensions,
// duplicate keys and other shapes retain the existing decoder semantics.
func decodeSimpleTextInput(raw json.RawMessage) ([]textgen.InputItem, bool) {
	if !startsJSONArray(raw) {
		return nil, false
	}
	parsed := gjson.ParseBytes(raw)
	valid := parsed.IsArray()
	parsed.ForEach(func(_, item gjson.Result) bool {
		if !item.IsObject() {
			valid = false
			return false
		}
		hasContent := false
		itemKeys := map[string]bool{}
		item.ForEach(func(key, value gjson.Result) bool {
			if itemKeys[key.Str] {
				valid = false
				return false
			}
			itemKeys[key.Str] = true
			switch key.Str {
			case "type":
				if value.Type != gjson.String || value.Str != "" && value.Str != "message" {
					valid = false
				}
			case "role":
				if value.Type != gjson.String {
					valid = false
					break
				}
				switch value.Str {
				case "", "system", "developer", "user", "assistant", "tool":
				default:
					valid = false
				}
			case "content":
				hasContent = true
				if !value.IsArray() {
					valid = false
					return false
				}
				value.ForEach(func(_, part gjson.Result) bool {
					if !part.IsObject() {
						valid = false
						return false
					}
					partKeys := map[string]bool{}
					part.ForEach(func(k, value gjson.Result) bool {
						if partKeys[k.Str] {
							valid = false
							return false
						}
						partKeys[k.Str] = true
						if (k.Str != "type" && k.Str != "text") || value.Type != gjson.String {
							valid = false
						}
						if k.Str == "type" && value.Str != "input_text" {
							valid = false
						}
						return valid
					})
					valid = valid && partKeys["type"] && partKeys["text"]
					return valid
				})
			default:
				valid = false
			}
			return valid
		})
		valid = valid && hasContent
		return valid
	})
	if !valid {
		return nil, false
	}
	var wire []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || len(wire) == 0 {
		return nil, false
	}
	items := make([]textgen.InputItem, 0, len(wire))
	for _, message := range wire {
		if message.Type != "" && message.Type != "message" {
			return nil, false
		}
		role := message.Role
		if role == "" {
			role = "user"
		}
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return nil, false
		}
		parts := make([]textgen.ContentPart, 0, len(message.Content))
		for _, part := range message.Content {
			if part.Type != "input_text" {
				return nil, false
			}
			parts = append(parts, textgen.ContentPart{Kind: textgen.ContentText, Text: part.Text})
		}
		items = append(items, textgen.InputItem{Kind: textgen.InputMessage, Role: role, Content: parts})
	}
	return items, true
}
