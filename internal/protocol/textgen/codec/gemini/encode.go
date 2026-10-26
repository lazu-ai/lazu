package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// SealedBy is the dialect Gemini's thought signatures are bound to. A
// signature goes back to Gemini with the part it came on and to nobody else.
const SealedBy = "gemini"

// EncodeOptions are what the encoder needs from its caller. InlineImage
// fetches an image named by an http(s) URL as (mime type, base64 data):
// Gemini takes images inline, and the fetch is the gateway's guarded one.
type EncodeOptions struct {
	InlineImage func(url string) (mimeType string, data string, err error)
}

// hostedByName are the function names Chat clients use for Gemini's own
// tools (the convention the Chat-to-Gemini conversion has always honoured).
var hostedByName = map[string]string{"googleSearch": "google_search", "codeExecution": "code_execution", "urlContext": "url_context"}

// Encode is the generateContent request for an IR request: the system
// instruction from the instructions (and any system turn), user and model
// contents with function calls and responses as parts, tools, tool choice,
// sampling, the JSON schema as responseSchema. Thought signatures go back
// only when Gemini sealed them. What is the channel's to decide is left to
// it: safety settings, thinking for the model, response modalities.
func Encode(request *textgen.Request, opts EncodeOptions) (*dto.GeminiChatRequest, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	out := &dto.GeminiChatRequest{Contents: make([]dto.GeminiChatContent, 0, len(request.Conversation))}
	encodeGeneration(request, &out.GenerationConfig)
	if err := encodeOutput(request.Output, &out.GenerationConfig); err != nil {
		return nil, err
	}
	var system []string
	for _, instruction := range request.Instructions {
		system = append(system, instruction.Text)
	}
	names := map[string]string{} // call id -> function name, for responses
	for _, item := range request.Conversation {
		if item.Kind == textgen.InputFunctionCall {
			names[item.CallID] = item.FunctionName
		}
	}
	for index, item := range request.Conversation {
		switch item.Kind {
		case textgen.InputMessage:
			if item.Role == "system" || item.Role == "developer" {
				// Gemini has one system instruction; a system turn joins it
				system = append(system, partsText(item.Content))
				continue
			}
			parts, err := encodeParts(item.Content, opts)
			if err != nil {
				return nil, fmt.Errorf("input[%d]: %w", index, err)
			}
			if len(parts) == 0 {
				continue
			}
			out.Contents = append(out.Contents, dto.GeminiChatContent{Role: roleOf(item.Role), Parts: parts})
		case textgen.InputFunctionCall:
			args := map[string]any{}
			if strings.TrimSpace(item.Arguments) != "" {
				if err := json.Unmarshal([]byte(item.Arguments), &args); err != nil {
					return nil, fmt.Errorf("invalid arguments for function %s, args: %s", item.FunctionName, item.Arguments)
				}
			}
			part := dto.GeminiPart{FunctionCall: &dto.FunctionCall{ID: item.CallID, FunctionName: item.FunctionName, Arguments: args}, ThoughtSignature: signatureOf(item.Opaque)}
			appendPart(out, "model", part)
		case textgen.InputFunctionResult:
			name := item.Name
			if name == "" {
				name = names[item.CallID]
			}
			part := dto.GeminiPart{FunctionResponse: &dto.GeminiFunctionResponse{ID: item.CallID, Name: name, Response: responseOf(textgen.ResultText(item))}}
			appendResult(out, part)
			images, err := encodeParts(item.Content, opts)
			if err != nil {
				return nil, fmt.Errorf("input[%d]: %w", index, err)
			}
			for _, image := range images {
				appendResult(out, image)
			}
		case textgen.InputReasoning:
			// a signature Gemini put on the part before it goes back on that
			// part; reasoning another vendor sealed, or plain reasoning, has
			// no place in a Gemini turn
			signature := signatureOf(item.Opaque)
			if signature == "" {
				continue
			}
			if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == "model" && len(out.Contents[n-1].Parts) > 0 {
				last := &out.Contents[n-1].Parts[len(out.Contents[n-1].Parts)-1]
				if last.ThoughtSignature == "" {
					last.ThoughtSignature = signature
					continue
				}
			}
			appendPart(out, "model", dto.GeminiPart{ThoughtSignature: signature})
		default:
			return nil, fmt.Errorf("input[%d]: %s items cannot be sent to Gemini", index, item.Kind)
		}
	}
	if len(system) > 0 {
		out.SystemInstructions = &dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: strings.Join(system, "\n")}}}
	}
	if err := encodeTools(request, out); err != nil {
		return nil, err
	}
	return out, nil
}

func roleOf(role string) string {
	if role == "assistant" || role == "model" {
		return "model"
	}
	return "user"
}

func partsText(parts []textgen.ContentPart) string {
	var texts []string
	for _, part := range parts {
		if part.Kind == textgen.ContentText {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "")
}

func signatureOf(opaque []textgen.OpaqueValue) string {
	for _, value := range opaque {
		if value.Dialect != SealedBy {
			continue
		}
		var signature string
		if json.Unmarshal(value.Value, &signature) == nil {
			return signature
		}
	}
	return ""
}

// appendPart adds a part to the last content when it has the role, else
// opens a content for it.
func appendPart(out *dto.GeminiChatRequest, role string, part dto.GeminiPart) {
	if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == role {
		out.Contents[n-1].Parts = append(out.Contents[n-1].Parts, part)
		return
	}
	out.Contents = append(out.Contents, dto.GeminiChatContent{Role: role, Parts: []dto.GeminiPart{part}})
}

// appendResult adds a function response to the user turn that answers the
// model's calls: after a model turn a user turn opens; otherwise the last
// turn takes it, as Gemini groups a turn's responses together.
func appendResult(out *dto.GeminiChatRequest, part dto.GeminiPart) {
	if n := len(out.Contents); n == 0 || out.Contents[n-1].Role == "model" {
		out.Contents = append(out.Contents, dto.GeminiChatContent{Role: "user"})
	}
	last := &out.Contents[len(out.Contents)-1]
	last.Parts = append(last.Parts, part)
}

// responseOf is a function result as Gemini's response object: a JSON
// object as it is, a JSON array under "result", anything else under
// "content".
func responseOf(output string) map[string]any {
	var object map[string]any
	if json.Unmarshal([]byte(output), &object) == nil && object != nil {
		return object
	}
	var array []any
	if json.Unmarshal([]byte(output), &array) == nil {
		return map[string]any{"result": array}
	}
	return map[string]any{"content": output}
}

func encodeParts(parts []textgen.ContentPart, opts EncodeOptions) ([]dto.GeminiPart, error) {
	var out []dto.GeminiPart
	for _, part := range parts {
		switch part.Kind {
		case textgen.ContentText:
			if part.Text == "" {
				continue
			}
			out = append(out, dto.GeminiPart{Text: part.Text})
		case textgen.ContentImage:
			mimeType, data, err := inline(part.URL, part.Data, opts)
			if err != nil {
				return nil, err
			}
			out = append(out, dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: mimeType, Data: data}})
		case textgen.ContentFile:
			if part.FileID != "" && part.Data == "" && part.URL == "" {
				return nil, errors.New("only base64 file is supported in gemini")
			}
			mimeType, data, err := inline(part.URL, part.Data, opts)
			if err != nil {
				return nil, err
			}
			out = append(out, dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: mimeType, Data: data}})
		case textgen.ContentAudio:
			if part.Data == "" {
				return nil, errors.New("only base64 audio is supported in gemini")
			}
			_, data, ok := splitDataURL(part.Data)
			if !ok {
				data = part.Data
			}
			out = append(out, dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: "audio/" + part.Format, Data: data}})
		}
	}
	return out, nil
}

// inline is media as Gemini's inline data: a data URL split, an http(s)
// URL fetched through the caller.
func inline(url, data string, opts EncodeOptions) (string, string, error) {
	source := url
	if source == "" {
		source = data
	}
	if mimeType, payload, ok := splitDataURL(source); ok {
		return mimeType, payload, nil
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if opts.InlineImage == nil {
			return "", "", fmt.Errorf("an image by URL needs fetching for Gemini: %s", source)
		}
		return opts.InlineImage(source)
	}
	return "", "", errors.New("media for Gemini must be a data URL or an http(s) URL")
}

func splitDataURL(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	header, payload, found := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !found {
		return "", "", false
	}
	mimeType := strings.TrimSuffix(header, ";base64")
	if i := strings.Index(mimeType, ";"); i >= 0 {
		mimeType = mimeType[:i]
	}
	return mimeType, payload, true
}

func encodeGeneration(request *textgen.Request, config *dto.GeminiChatGenerationConfig) {
	sampling := request.Sampling
	if sampling.Temperature.Set {
		value := sampling.Temperature.Value
		config.Temperature = &value
	}
	if sampling.TopP.Set {
		config.TopP = sampling.TopP.Value
	}
	if sampling.TopK.Set {
		config.TopK = float64(sampling.TopK.Value)
	}
	if sampling.MaxOutput.Set {
		config.MaxOutputTokens = sampling.MaxOutput.Value
	}
	if sampling.Seed.Set {
		config.Seed = sampling.Seed.Value
	}
	if sampling.N.Set && sampling.N.Value > 1 {
		config.CandidateCount = sampling.N.Value
	}
	if sampling.Stop.Set {
		var one string
		var many []string
		switch {
		case json.Unmarshal(sampling.Stop.Value, &one) == nil && one != "":
			config.StopSequences = []string{one}
		case json.Unmarshal(sampling.Stop.Value, &many) == nil && len(many) > 0:
			config.StopSequences = many
		}
	}
}

func encodeOutput(output textgen.OutputConstraint, config *dto.GeminiChatGenerationConfig) error {
	switch output.Type {
	case "json_object":
		config.ResponseMimeType = "application/json"
	case "json_schema":
		config.ResponseMimeType = "application/json"
		if len(output.Schema) > 0 {
			var schema any
			if err := json.Unmarshal(output.Schema, &schema); err != nil {
				return fmt.Errorf("response_format.json_schema.schema: %w", err)
			}
			config.ResponseSchema = CleanResponseSchema(schema, 0)
		}
	}
	return nil
}

// encodeTools is the tools in Gemini's order (code execution, search, URL
// context, then the function declarations) and the tool choice when it is
// not Gemini's default (auto).
func encodeTools(request *textgen.Request, out *dto.GeminiChatRequest) error {
	if len(request.Tools) == 0 {
		return nil
	}
	var functions []dto.FunctionRequest
	hosted := map[string]bool{}
	for index, tool := range request.Tools {
		kind := tool.Type
		if kind == "function" {
			if name, ok := hostedByName[tool.Name]; ok {
				kind = name
			}
		}
		switch kind {
		case "function":
			var parameters any
			if len(tool.Parameters) > 0 {
				if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
					return fmt.Errorf("tools[%d].parameters: %w", index, err)
				}
				if object, ok := parameters.(map[string]any); ok {
					if properties, ok := object["properties"].(map[string]any); ok && len(properties) == 0 {
						parameters = nil
					}
				}
			}
			functions = append(functions, dto.FunctionRequest{Name: tool.Name, Description: tool.Description, Parameters: CleanFunctionParameters(parameters)})
		case "web_search", "web_search_preview", "google_search":
			hosted["google_search"] = true
		case "code_execution", "url_context":
			hosted[kind] = true
		default:
			return fmt.Errorf("tools[%d]: %q tools cannot be sent to Gemini", index, tool.Type)
		}
	}
	var tools []dto.GeminiChatTool
	if hosted["code_execution"] {
		tools = append(tools, dto.GeminiChatTool{CodeExecution: map[string]string{}})
	}
	if hosted["google_search"] {
		tools = append(tools, dto.GeminiChatTool{GoogleSearch: map[string]string{}})
	}
	if hosted["url_context"] {
		tools = append(tools, dto.GeminiChatTool{URLContext: map[string]string{}})
	}
	if len(functions) > 0 {
		tools = append(tools, dto.GeminiChatTool{FunctionDeclarations: functions})
	}
	out.SetTools(tools)
	switch request.ToolPolicy.Mode {
	case "required":
		out.ToolConfig = &dto.ToolConfig{FunctionCallingConfig: &dto.FunctionCallingConfig{Mode: "ANY"}}
	case "none":
		out.ToolConfig = &dto.ToolConfig{FunctionCallingConfig: &dto.FunctionCallingConfig{Mode: "NONE"}}
	case "function":
		if request.ToolPolicy.FunctionName != "" {
			out.ToolConfig = &dto.ToolConfig{FunctionCallingConfig: &dto.FunctionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{request.ToolPolicy.FunctionName}}}
		}
	}
	return nil
}
