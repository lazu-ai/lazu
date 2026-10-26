package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// knownTopLevel are the generateContent request fields the IR models.
// safetySettings is advisory: it tunes Google's own filters and has no
// counterpart elsewhere, so a bridge leaves it out rather than refusing.
// Anything else (cachedContent, labels, ...) is an unknown field: kept for a
// native relay, refused by a bridge.
var knownTopLevel = map[string]struct{}{
	"contents": {}, "systemInstruction": {}, "system_instruction": {}, "tools": {}, "toolConfig": {}, "tool_config": {},
	"generationConfig": {}, "generation_config": {}, "safetySettings": {}, "safety_settings": {},
}

// knownGeneration are the generationConfig fields the IR models; thinking
// display (includeThoughts) is advisory.
var knownGeneration = map[string]struct{}{
	"temperature": {}, "topP": {}, "topK": {}, "maxOutputTokens": {}, "stopSequences": {}, "candidateCount": {},
	"seed": {}, "responseMimeType": {}, "responseSchema": {}, "responseJsonSchema": {}, "thinkingConfig": {},
	"responseLogprobs": {}, "logprobs": {},
}

// Decode is a generateContent request (a Gemini client's body) as the IR,
// beside the wire DTO a native relay sends. The model and whether it
// streams are in the URL, not the body: the caller gives the model and sets
// the stream. What the IR does not model is an unknown field at its path.
func Decode(body []byte, model string) (*textgen.Request, *dto.GeminiChatRequest, error) {
	var wire dto.GeminiChatRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, err
	}
	if len(wire.Contents) == 0 {
		return nil, nil, fmt.Errorf("contents is required")
	}
	raw, err := textgen.RetainTopLevelFields(body)
	if err != nil {
		return nil, nil, err
	}
	request := &textgen.Request{
		Model: textgen.ModelRef{Canonical: model},
		Raw:   textgen.RawRetention{Protocol: textgen.ProtocolGemini, TopLevel: raw, UnknownFields: map[string]json.RawMessage{}},
	}
	for key, value := range raw {
		if _, ok := knownTopLevel[key]; !ok {
			request.Raw.UnknownFields[key] = append(json.RawMessage(nil), value...)
		}
	}
	if wire.SystemInstructions != nil {
		var texts []string
		for _, part := range wire.SystemInstructions.Parts {
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
		if len(texts) > 0 {
			request.Instructions = append(request.Instructions, textgen.Instruction{Role: "system", Text: strings.Join(texts, "\n")})
		}
	}
	if err := decodeContents(wire.Contents, request); err != nil {
		return nil, nil, err
	}
	decodeTools(&wire, request)
	decodeToolConfig(wire.ToolConfig, request)
	decodeGeneration(raw, &wire.GenerationConfig, request)
	if len(request.Raw.UnknownFields) == 0 {
		request.Raw.UnknownFields = nil
	}
	return request, &wire, nil
}

func unknown(request *textgen.Request, path string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = json.RawMessage("null")
	}
	request.Raw.UnknownFields[path] = encoded
}

// decodeContents is the turns as IR items. A function response answers the
// earliest open call of its name (Gemini pairs them by name, Chat by id); a
// call without an id gets one no other call has.
func decodeContents(contents []dto.GeminiChatContent, request *textgen.Request) error {
	used := map[string]bool{}
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil && part.FunctionCall.ID != "" {
				if used[part.FunctionCall.ID] {
					return fmt.Errorf("duplicate Gemini functionCall.id %q cannot be faithfully relayed", part.FunctionCall.ID)
				}
				used[part.FunctionCall.ID] = true
			}
		}
	}
	next := 1
	type pending struct{ id, name string }
	var open []pending
	for index, content := range contents {
		role := "user"
		if content.Role == "model" {
			role = "assistant"
		}
		var parts []textgen.ContentPart
		flush := func() {
			if len(parts) > 0 {
				request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: role, Content: parts})
				parts = nil
			}
		}
		prompt := false
		for partIndex, part := range content.Parts {
			path := fmt.Sprintf("contents[%d].parts[%d]", index, partIndex)
			switch {
			case part.FunctionCall != nil:
				flush()
				id := part.FunctionCall.ID
				for id == "" {
					candidate := fmt.Sprintf("call_%d", next)
					next++
					if !used[candidate] {
						id, used[candidate] = candidate, true
					}
				}
				item := textgen.InputItem{Kind: textgen.InputFunctionCall, Role: "assistant", CallID: id, FunctionName: part.FunctionCall.FunctionName, Arguments: argumentsOf(part.FunctionCall.Arguments)}
				if part.ThoughtSignature != "" {
					item.Opaque = []textgen.OpaqueValue{sealed(part.ThoughtSignature)}
				}
				request.Conversation = append(request.Conversation, item)
				open = append(open, pending{id: id, name: part.FunctionCall.FunctionName})
			case part.FunctionResponse != nil:
				flush()
				// by the call's id when the response names one, else the
				// first open call of that name, else the first open call
				match := -1
				if responseID := part.FunctionResponse.ID; responseID != "" {
					for i, call := range open {
						if call.id == responseID {
							match = i
							break
						}
					}
				}
				for i, call := range open {
					if match >= 0 {
						break
					}
					if call.name == part.FunctionResponse.Name {
						match = i
					}
				}
				if match < 0 && len(open) > 0 {
					match = 0
				}
				if match < 0 {
					return fmt.Errorf("function response %q has no preceding function call", part.FunctionResponse.Name)
				}
				id := open[match].id
				open = append(open[:match], open[match+1:]...)
				output, _ := json.Marshal(part.FunctionResponse.Response)
				request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionResult, Role: "tool", CallID: id, Output: string(output)})
			case part.Thought:
				flush()
				item := textgen.InputItem{Kind: textgen.InputReasoning, Role: "assistant", Opaque: []textgen.OpaqueValue{sealed(part.ThoughtSignature)}}
				if part.Text != "" {
					item.PlainReasoning = textgen.Some(part.Text)
				}
				request.Conversation = append(request.Conversation, item)
			default:
				if content.Role == "user" {
					prompt = true
				}
				if media, ok := mediaOf(part, path, request); ok {
					parts = append(parts, media)
				} else if part.Text != "" {
					parts = append(parts, textgen.ContentPart{Kind: textgen.ContentText, Text: part.Text})
				}
				if part.ThoughtSignature != "" {
					// a signature on a text part goes back on that part
					flush()
					request.Conversation = append(request.Conversation, textgen.InputItem{Kind: textgen.InputReasoning, Role: "assistant", Opaque: []textgen.OpaqueValue{sealed(part.ThoughtSignature)}})
				}
			}
		}
		flush()
		if prompt {
			open = nil // a new prompt closes the calls left unanswered
		}
	}
	return nil
}

func sealed(signature string) textgen.OpaqueValue {
	encoded, _ := json.Marshal(signature)
	return textgen.OpaqueValue{Dialect: SealedBy, Value: encoded}
}

// mediaOf is inline or file data as a content part: an image, audio or a
// file by its mime type.
func mediaOf(part dto.GeminiPart, path string, request *textgen.Request) (textgen.ContentPart, bool) {
	switch {
	case part.InlineData != nil:
		mimeType := strings.ToLower(part.InlineData.MimeType)
		url := "data:" + part.InlineData.MimeType + ";base64," + part.InlineData.Data
		switch {
		case strings.HasPrefix(mimeType, "image/"):
			return textgen.ContentPart{Kind: textgen.ContentImage, URL: url}, true
		case strings.HasPrefix(mimeType, "audio/"):
			return textgen.ContentPart{Kind: textgen.ContentAudio, Data: part.InlineData.Data, Format: strings.TrimPrefix(mimeType, "audio/")}, true
		}
		return textgen.ContentPart{Kind: textgen.ContentFile, Data: url, Format: part.InlineData.MimeType}, true
	case part.FileData != nil:
		if strings.HasPrefix(strings.ToLower(part.FileData.MimeType), "image/") {
			return textgen.ContentPart{Kind: textgen.ContentImage, URL: part.FileData.FileUri}, true
		}
		unknown(request, path+".fileData", part.FileData)
	case part.ExecutableCode != nil:
		unknown(request, path+".executableCode", part.ExecutableCode)
	case part.CodeExecutionResult != nil:
		unknown(request, path+".codeExecutionResult", part.CodeExecutionResult)
	}
	return textgen.ContentPart{}, false
}

func decodeTools(wire *dto.GeminiChatRequest, request *textgen.Request) {
	declarations := rawFunctionDeclarations(wire.Tools)
	for index, tool := range wire.GetTools() {
		path := fmt.Sprintf("tools[%d]", index)
		if tool.FunctionDeclarations != nil {
			// read from the client's bytes: the typed tool keeps only the
			// OpenAPI-subset parameters
			raw := declarations[index]
			if len(raw) == 0 {
				raw, _ = json.Marshal(tool.FunctionDeclarations)
			}
			var functions []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
				// a plain JSON Schema, which newer clients (Gemini CLI)
				// send in place of the OpenAPI-subset parameters
				ParametersJsonSchema json.RawMessage `json:"parametersJsonSchema"`
			}
			if json.Unmarshal(raw, &functions) != nil {
				var function struct {
					Name                 string          `json:"name"`
					Description          string          `json:"description"`
					Parameters           json.RawMessage `json:"parameters"`
					ParametersJsonSchema json.RawMessage `json:"parametersJsonSchema"`
				}
				if json.Unmarshal(raw, &function) != nil {
					unknown(request, path+".functionDeclarations", tool.FunctionDeclarations)
				} else {
					functions = append(functions, function)
				}
			}
			for _, function := range functions {
				parameters := nonNull(function.Parameters)
				if len(parameters) == 0 {
					parameters = nonNull(function.ParametersJsonSchema)
				}
				request.Tools = append(request.Tools, textgen.ToolDefinition{Type: "function", Name: function.Name, Description: function.Description, Parameters: parameters})
			}
		}
		if tool.GoogleSearch != nil {
			hosted, _ := json.Marshal(map[string]any{"googleSearch": tool.GoogleSearch})
			request.Tools = append(request.Tools, textgen.ToolDefinition{Type: "web_search", Hosted: hosted})
		}
		for kind, value := range map[string]any{"code_execution": tool.CodeExecution, "url_context": tool.URLContext, "google_search_retrieval": tool.GoogleSearchRetrieval} {
			if value != nil {
				hosted, _ := json.Marshal(value)
				request.Tools = append(request.Tools, textgen.ToolDefinition{Type: kind, Hosted: hosted})
			}
		}
	}
}

// rawFunctionDeclarations is each tool's functionDeclarations as the client
// sent it, by tool index (tools is an array or a single object).
func rawFunctionDeclarations(tools json.RawMessage) map[int]json.RawMessage {
	var list []map[string]json.RawMessage
	if json.Unmarshal(tools, &list) != nil {
		var single map[string]json.RawMessage
		if json.Unmarshal(tools, &single) != nil {
			return nil
		}
		list = []map[string]json.RawMessage{single}
	}
	out := make(map[int]json.RawMessage, len(list))
	for index, tool := range list {
		if raw, ok := tool["functionDeclarations"]; ok {
			out[index] = raw
		} else if raw, ok := tool["function_declarations"]; ok {
			out[index] = raw
		}
	}
	return out
}

func nonNull(raw json.RawMessage) json.RawMessage {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return raw
}

func decodeToolConfig(config *dto.ToolConfig, request *textgen.Request) {
	if config == nil {
		return
	}
	if config.RetrievalConfig != nil {
		unknown(request, "toolConfig.retrievalConfig", config.RetrievalConfig)
	}
	calling := config.FunctionCallingConfig
	if calling == nil {
		return
	}
	switch strings.ToUpper(string(calling.Mode)) {
	case "", "AUTO":
		request.ToolPolicy.Mode = "auto"
	case "ANY":
		request.ToolPolicy.Mode = "required"
		if len(calling.AllowedFunctionNames) == 1 {
			request.ToolPolicy.Mode = "function"
			request.ToolPolicy.FunctionName = calling.AllowedFunctionNames[0]
		} else if len(calling.AllowedFunctionNames) > 1 {
			unknown(request, "toolConfig.functionCallingConfig.allowedFunctionNames", calling.AllowedFunctionNames)
		}
	case "NONE":
		request.ToolPolicy.Mode = "none"
	default:
		unknown(request, "toolConfig.functionCallingConfig.mode", calling.Mode)
	}
}

func decodeGeneration(raw map[string]json.RawMessage, config *dto.GeminiChatGenerationConfig, request *textgen.Request) {
	value, ok := raw["generationConfig"]
	if !ok {
		value = raw["generation_config"]
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(value, &fields)
	for key, field := range fields {
		if _, known := knownGeneration[key]; !known && !bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
			request.Raw.UnknownFields["generationConfig."+key] = append(json.RawMessage(nil), field...)
		}
	}
	// an explicit zero is a value, not an omission: presence is read off
	// the raw config
	present := func(names ...string) bool {
		for _, name := range names {
			if field, ok := fields[name]; ok && !bytes.Equal(bytes.TrimSpace(field), []byte("null")) {
				return true
			}
		}
		return false
	}
	sampling := &request.Sampling
	if config.Temperature != nil {
		sampling.Temperature = textgen.Some(*config.Temperature)
	}
	if config.TopP > 0 || present("topP", "top_p") {
		sampling.TopP = textgen.Some(config.TopP)
	}
	if config.TopK > 0 || present("topK", "top_k") {
		sampling.TopK = textgen.Some(int(config.TopK))
	}
	if config.MaxOutputTokens > 0 {
		sampling.MaxOutput = textgen.Some(config.MaxOutputTokens)
	}
	if len(config.StopSequences) > 0 {
		encoded, _ := json.Marshal(config.StopSequences)
		sampling.Stop = textgen.Some(json.RawMessage(encoded))
	}
	if config.CandidateCount > 0 {
		sampling.N = textgen.Some(config.CandidateCount)
	}
	if config.Seed != 0 || present("seed") {
		sampling.Seed = textgen.Some(config.Seed)
	}
	if config.ResponseLogprobs {
		sampling.LogProbs = textgen.Some(true)
	}
	if config.Logprobs != nil {
		sampling.TopLogProbs = textgen.Some(int(*config.Logprobs))
	}
	if strings.EqualFold(config.ResponseMimeType, "application/json") {
		request.Output = textgen.OutputConstraint{Type: "json_object"}
		schema := config.ResponseJsonSchema
		if len(schema) == 0 && config.ResponseSchema != nil {
			// Gemini's Schema, not JSON Schema: its type enums are upper
			// case (OBJECT) and nullable is a flag
			schema, _ = json.Marshal(jsonSchemaOf(config.ResponseSchema))
		}
		if len(schema) > 0 {
			request.Output = textgen.OutputConstraint{Type: "json_schema", Name: "response", Schema: schema}
		}
	} else if config.ResponseMimeType != "" && !strings.EqualFold(config.ResponseMimeType, "text/plain") {
		unknown(request, "generationConfig.responseMimeType", config.ResponseMimeType)
	}
	if thinking := config.ThinkingConfig; thinking != nil {
		switch {
		case thinking.ThinkingLevel != "":
			request.Reasoning.Effort = strings.ToLower(thinking.ThinkingLevel)
		case thinking.ThinkingBudget != nil && *thinking.ThinkingBudget == 0:
			request.Reasoning.Effort = "none"
		case thinking.ThinkingBudget != nil && *thinking.ThinkingBudget > 0:
			request.Reasoning.Budget = textgen.Some(*thinking.ThinkingBudget)
		}
		// a budget of -1 (dynamic) is the model's default: nothing to ask
		if thinking.IncludeThoughts {
			// the thoughts shown: a reasoning summary, which a bridge must
			// carry or refuse
			request.Reasoning.Summary = "auto"
		}
	}
}

// jsonSchemaOf is a Gemini Schema as JSON Schema: type names in lower case,
// nullable as a null type, propertyOrdering (Gemini's own) left out.
func jsonSchemaOf(schema any) any {
	raw, err := json.Marshal(schema)
	if err != nil {
		return schema
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return schema
	}
	var convert func(any) any
	convert = func(node any) any {
		switch typed := node.(type) {
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, child := range typed {
				if properties, ok := child.(map[string]any); ok && key == "properties" {
					// property names, each with its schema: names are
					// never keywords
					named := make(map[string]any, len(properties))
					for name, property := range properties {
						named[name] = convert(property)
					}
					out[key] = named
					continue
				}
				switch key {
				case "propertyOrdering", "nullable":
					continue
				case "type":
					if name, ok := child.(string); ok {
						out[key] = strings.ToLower(name)
						continue
					}
				}
				out[key] = convert(child)
			}
			if nullable, _ := typed["nullable"].(bool); nullable {
				if name, ok := out["type"].(string); ok {
					out["type"] = []any{name, "null"}
				}
			}
			return out
		case []any:
			out := make([]any, len(typed))
			for i, child := range typed {
				out[i] = convert(child)
			}
			return out
		}
		return node
	}
	return convert(value)
}
