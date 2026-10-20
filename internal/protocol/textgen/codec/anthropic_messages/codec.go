package anthropic_messages

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// EncodeOptions is what the encoder needs from the channel.
type EncodeOptions struct {
	// InlineImage fetches an http(s) image so it goes as base64, for a
	// channel that takes no url image source (Bedrock, Vertex). nil sends
	// url sources, which Anthropic's own API takes.
	InlineImage func(url string) (mediaType, base64Data string, err error)
}

// placeholderText stands in for a message that would otherwise be empty:
// the Messages API refuses empty content and empty text blocks.
const placeholderText = "..."

// interruptedResult answers a tool_use the conversation never answered, as
// the Messages API refuses an unanswered call.
const interruptedResult = "The tool call was interrupted before it returned a result."

// Encode is the Messages request for an IR request: system from the
// instructions, alternating user and assistant turns with tool calls and
// results as blocks, tools, tool choice, sampling, stop sequences, the JSON
// schema as output_config.format, the effort as adaptive thinking (the
// channel fits it to the model after: claude.PrepareNativeThinking). Prior
// reasoning goes back only when Anthropic sealed it (a thinking block with
// its signature, a redacted_thinking block); another dialect's reasoning
// has no place in a Messages turn and is left out.
func Encode(request *textgen.Request, opts EncodeOptions) (*dto.ClaudeRequest, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	model := request.Model.Upstream
	if model == "" {
		model = request.Model.Canonical
	}
	out := &dto.ClaudeRequest{Model: model, Stream: request.Lifecycle.Stream}
	if system := encodeSystem(request.Instructions); len(system) > 0 {
		out.System = system
	}
	messages, err := encodeMessages(request.Conversation, opts)
	if err != nil {
		return nil, err
	}
	out.Messages = messages
	if request.Sampling.MaxOutput.Set {
		out.MaxTokens = request.Sampling.MaxOutput.Value
	}
	if request.Sampling.Temperature.Set {
		value := request.Sampling.Temperature.Value
		out.Temperature = &value
	}
	if request.Sampling.TopP.Set {
		out.TopP = request.Sampling.TopP.Value
	}
	if request.Sampling.TopK.Set {
		out.TopK = request.Sampling.TopK.Value
	}
	if request.Sampling.Stop.Set {
		out.StopSequences = stopSequences(request.Sampling.Stop.Value)
	}
	tools, err := encodeTools(request.Tools)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		out.Tools = tools
		if choice := encodeToolChoice(request.ToolPolicy); choice != nil {
			out.ToolChoice = choice // the field is untyped; a nil pointer in it would be written as null
		}
	}
	if request.Output.Type == "json_schema" && len(request.Output.Schema) > 0 {
		format, _ := json.Marshal(map[string]any{"format": map[string]any{"type": "json_schema", "schema": json.RawMessage(request.Output.Schema)}})
		out.OutputConfig = format
	}
	effort := strings.ToLower(strings.TrimSpace(request.Reasoning.Effort))
	switch {
	case effort == "none" || effort == "off" || effort == "disabled":
		out.Thinking = &dto.Thinking{Type: "disabled"}
	case request.Reasoning.Budget.Set:
		// the budget shape; an effort beside it names the effort an adaptive
		// model takes instead (the channel fits it: claude.NormalizeThinking)
		budget := request.Reasoning.Budget.Value
		out.Thinking = &dto.Thinking{Type: "enabled", BudgetTokens: &budget}
		if effort != "" {
			out.OutputConfig = withOutputConfigEffort(out.OutputConfig, effort)
		}
	case effort != "":
		// the generic adaptive shape; the channel fits it to the model
		out.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
		out.OutputConfig = withOutputConfigEffort(out.OutputConfig, effort)
	}
	if userID := metadataUserID(request.Metadata); userID != "" {
		metadata, _ := json.Marshal(map[string]string{"user_id": userID})
		out.Metadata = metadata
	}
	out.ServiceTier = request.Metadata.ServiceTier
	out.CacheControl = request.Vendor.CacheControl
	out.Speed = request.Vendor.Speed
	return out, nil
}

func withOutputConfigEffort(raw json.RawMessage, effort string) json.RawMessage {
	config := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &config)
	}
	config["effort"] = effort
	out, _ := json.Marshal(config)
	return out
}

func stopSequences(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one == "" {
			return nil
		}
		return []string{one}
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many
	}
	return nil
}

// encodeSystem is the instructions as the system prompt: one text block
// each, in order, empty ones left out. Chat's developer role is a system
// role to Messages, which has one.
func encodeSystem(instructions []textgen.Instruction) []dto.ClaudeMediaMessage {
	var out []dto.ClaudeMediaMessage
	for _, instruction := range instructions {
		if strings.TrimSpace(instruction.Text) == "" {
			continue
		}
		text := instruction.Text
		out = append(out, dto.ClaudeMediaMessage{Type: "text", Text: &text, CacheControl: instruction.CacheControl})
	}
	return out
}

// encodeMessages is the conversation as Messages turns: consecutive items
// of one role fold into one message (Messages alternates roles), tool
// results go into the user message after their call, and the first turn is
// the user's. Then the tool exchange is mended (pairToolBlocks).
func encodeMessages(items []textgen.InputItem, opts EncodeOptions) ([]dto.ClaudeMessage, error) {
	var messages []dto.ClaudeMessage
	push := func(role string, blocks ...dto.ClaudeMediaMessage) {
		if len(blocks) == 0 {
			return
		}
		if n := len(messages); n > 0 && messages[n-1].Role == role {
			existing, _ := messages[n-1].Content.([]dto.ClaudeMediaMessage)
			messages[n-1].Content = append(existing, blocks...)
			return
		}
		messages = append(messages, dto.ClaudeMessage{Role: role, Content: blocks})
	}
	for index, item := range items {
		switch item.Kind {
		case textgen.InputMessage:
			role := "user"
			if item.Role == "assistant" {
				role = "assistant"
			}
			blocks, err := encodeContent(item.Content, opts)
			if err != nil {
				return nil, fmt.Errorf("input[%d]: %w", index, err)
			}
			if len(blocks) == 0 {
				text := placeholderText
				blocks = []dto.ClaudeMediaMessage{{Type: "text", Text: &text}}
			}
			push(role, blocks...)
		case textgen.InputFunctionCall:
			var input any = map[string]any{}
			if strings.TrimSpace(item.Arguments) != "" {
				var parsed any
				if err := json.Unmarshal([]byte(item.Arguments), &parsed); err == nil {
					input = parsed
				} else {
					input = map[string]any{"input": item.Arguments}
				}
			}
			push("assistant", dto.ClaudeMediaMessage{Type: "tool_use", Id: item.CallID, Name: item.FunctionName, Input: input})
		case textgen.InputFunctionResult:
			push("user", dto.ClaudeMediaMessage{Type: "tool_result", ToolUseId: item.CallID, Content: item.Output})
		case textgen.InputReasoning:
			for _, opaque := range item.Opaque {
				if opaque.Dialect != SealedBy || len(opaque.Value) == 0 {
					continue
				}
				var block dto.ClaudeMediaMessage
				if err := json.Unmarshal(opaque.Value, &block); err == nil && (block.Type == "thinking" || block.Type == "redacted_thinking") {
					push("assistant", block)
				}
			}
		default:
			return nil, fmt.Errorf("input[%d]: %s items cannot be sent to the Messages API", index, item.Kind)
		}
	}
	if len(messages) > 0 && messages[0].Role != "user" {
		text := placeholderText
		messages = append([]dto.ClaudeMessage{{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: &text}}}}, messages...)
	}
	return pairToolBlocks(messages), nil
}

func encodeContent(parts []textgen.ContentPart, opts EncodeOptions) ([]dto.ClaudeMediaMessage, error) {
	var blocks []dto.ClaudeMediaMessage
	for _, part := range parts {
		switch part.Kind {
		case textgen.ContentText:
			if part.Text == "" {
				continue
			}
			text := part.Text
			blocks = append(blocks, dto.ClaudeMediaMessage{Type: "text", Text: &text, CacheControl: part.CacheControl})
		case textgen.ContentImage:
			source, err := imageSource(part, opts)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, dto.ClaudeMediaMessage{Type: "image", Source: source, CacheControl: part.CacheControl})
		default:
			return nil, fmt.Errorf("%s content cannot be sent to the Messages API", part.Kind)
		}
	}
	return blocks, nil
}

// imageSource is a Messages image source for an image part: a data: URL
// as base64, an http(s) URL as a url source (or inlined when the channel
// asks), a vendor file id refused.
func imageSource(part textgen.ContentPart, opts EncodeOptions) (*dto.ClaudeMessageSource, error) {
	if part.FileID != "" && part.URL == "" && part.Data == "" {
		return nil, errors.New("an image named by a file id cannot be sent to the Messages API")
	}
	if part.Data != "" {
		mediaType := part.Format
		if mediaType == "" {
			mediaType = "image/png"
		}
		return &dto.ClaudeMessageSource{Type: "base64", MediaType: mediaType, Data: part.Data}, nil
	}
	url := part.URL
	if strings.HasPrefix(url, "data:") {
		mediaType, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
		if !ok {
			return nil, errors.New("malformed data URL image")
		}
		mediaType = strings.TrimSuffix(mediaType, ";base64")
		return &dto.ClaudeMessageSource{Type: "base64", MediaType: mediaType, Data: data}, nil
	}
	if opts.InlineImage != nil {
		mediaType, data, err := opts.InlineImage(url)
		if err != nil {
			return nil, err
		}
		return &dto.ClaudeMessageSource{Type: "base64", MediaType: mediaType, Data: data}, nil
	}
	return &dto.ClaudeMessageSource{Type: "url", Url: url}, nil
}

// pairToolBlocks mends the tool exchange as the Messages API requires: a
// tool_use the next message does not answer gets a synthetic error result
// there (or in a user message of its own), a tool_result naming no call of
// the message before becomes a text block, a second answer to one call is
// dropped. A well-formed exchange is returned as it was.
func pairToolBlocks(messages []dto.ClaudeMessage) []dto.ClaudeMessage {
	out := make([]dto.ClaudeMessage, 0, len(messages))
	var pending []string
	open := map[string]bool{}
	answered := map[string]bool{}
	synthetic := func(id string) dto.ClaudeMediaMessage {
		return dto.ClaudeMediaMessage{Type: "tool_result", ToolUseId: id, Content: interruptedResult}
	}
	closeExchange := func() {
		var blocks []dto.ClaudeMediaMessage
		for _, id := range pending {
			if !answered[id] {
				blocks = append(blocks, synthetic(id))
			}
		}
		if len(blocks) > 0 {
			out = append(out, dto.ClaudeMessage{Role: "user", Content: blocks})
		}
		pending, open, answered = nil, map[string]bool{}, map[string]bool{}
	}
	for _, message := range messages {
		blocks, _ := message.Content.([]dto.ClaudeMediaMessage)
		if message.Role == "assistant" {
			closeExchange()
			for _, block := range blocks {
				if block.Type == "tool_use" {
					pending = append(pending, block.Id)
					open[block.Id] = true
				}
			}
			out = append(out, message)
			continue
		}
		var kept, rest []dto.ClaudeMediaMessage
		for _, block := range blocks {
			if block.Type != "tool_result" {
				rest = append(rest, block)
				continue
			}
			if !open[block.ToolUseId] {
				rest = append(rest, resultAsText(block))
				continue
			}
			if answered[block.ToolUseId] {
				continue
			}
			answered[block.ToolUseId] = true
			kept = append(kept, block)
		}
		for _, id := range pending {
			if !answered[id] {
				kept = append(kept, synthetic(id))
			}
		}
		out = append(out, dto.ClaudeMessage{Role: "user", Content: append(kept, rest...)})
		pending, open, answered = nil, map[string]bool{}, map[string]bool{}
	}
	closeExchange()
	return out
}

func resultAsText(block dto.ClaudeMediaMessage) dto.ClaudeMediaMessage {
	text := "Tool result received."
	switch content := block.Content.(type) {
	case string:
		if strings.TrimSpace(content) != "" {
			text = content
		}
	default:
		if raw, err := json.Marshal(content); err == nil && len(raw) > 0 && string(raw) != "null" {
			text = string(raw)
		}
	}
	return dto.ClaudeMediaMessage{Type: "text", Text: &text}
}

func encodeTools(tools []textgen.ToolDefinition) ([]any, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]any, 0, len(tools))
	for index, tool := range tools {
		switch tool.Type {
		case "function":
			schema := map[string]any{"type": "object", "properties": map[string]any{}}
			if len(tool.Parameters) > 0 {
				var parsed map[string]any
				if err := json.Unmarshal(tool.Parameters, &parsed); err != nil {
					return nil, fmt.Errorf("tools[%d].parameters: %w", index, err)
				}
				if parsed != nil {
					schema = parsed
				}
			}
			out = append(out, &dto.Tool{Name: tool.Name, Description: tool.Description, InputSchema: schema, CacheControl: tool.CacheControl})
		case "web_search", "web_search_preview":
			out = append(out, &dto.ClaudeWebSearchTool{Type: "web_search_20250305", Name: "web_search", MaxUses: 5})
		default:
			return nil, fmt.Errorf("tools[%d]: %q tools cannot be sent to the Messages API", index, tool.Type)
		}
	}
	return out, nil
}

// encodeToolChoice is the tool policy in Messages' words; nil when the
// default (auto, parallel allowed) says it.
func encodeToolChoice(policy textgen.ToolPolicy) *dto.ClaudeToolChoice {
	disableParallel := policy.ParallelToolCalls.Set && !policy.ParallelToolCalls.Value
	var choice *dto.ClaudeToolChoice
	switch policy.Mode {
	case "":
		if disableParallel {
			choice = &dto.ClaudeToolChoice{Type: "auto"}
		}
	case "auto":
		choice = &dto.ClaudeToolChoice{Type: "auto"}
	case "required":
		choice = &dto.ClaudeToolChoice{Type: "any"}
	case "none":
		return &dto.ClaudeToolChoice{Type: "none"}
	default:
		if policy.FunctionName != "" {
			choice = &dto.ClaudeToolChoice{Type: "tool", Name: policy.FunctionName}
		}
	}
	if choice != nil && disableParallel {
		choice.DisableParallelToolUse = true
	}
	return choice
}

// DecodeMessage is the events of a whole (non-streaming) Messages response:
// what the stream decoder would have said, delivered at once.
func DecodeMessage(body []byte) ([]textgen.Event, error) {
	var message struct {
		ID         string            `json:"id"`
		Model      string            `json:"model"`
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
		Usage      *dto.ClaudeUsage  `json:"usage"`
		Error      *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, err
	}
	if message.Error != nil {
		return []textgen.Event{{Kind: textgen.EventError, Error: &textgen.SemanticError{Type: message.Error.Type, Code: "upstream_error", Message: message.Error.Message}}}, nil
	}
	events := []textgen.Event{{Kind: textgen.EventStart, ResponseID: message.ID, Model: message.Model}}
	for index, raw := range message.Content {
		var block contentBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		switch block.Type {
		case "text":
			if block.Text != "" {
				events = append(events, textgen.Event{Kind: textgen.EventText, Text: block.Text})
			}
		case "thinking":
			events = append(events, textgen.Event{Kind: textgen.EventReasoningStart})
			if block.Thinking != "" {
				events = append(events, textgen.Event{Kind: textgen.EventReasoning, Text: block.Thinking})
			}
			if block.Signature != "" {
				events = append(events, textgen.Event{Kind: textgen.EventReasoningSignature, Text: block.Signature, SealedBy: SealedBy})
			}
		case "redacted_thinking":
			events = append(events, textgen.Event{Kind: textgen.EventReasoningSealed, Sealed: append(json.RawMessage(nil), raw...), SealedBy: SealedBy})
		case "tool_use":
			events = append(events, textgen.Event{Kind: textgen.EventToolStart, ToolIndex: index, ToolCallID: block.ID, ToolName: block.Name})
			if len(block.Input) > 0 && string(block.Input) != "null" {
				events = append(events, textgen.Event{Kind: textgen.EventToolDone, ToolIndex: index, Text: string(block.Input)})
			} else {
				events = append(events, textgen.Event{Kind: textgen.EventToolDone, ToolIndex: index})
			}
		}
	}
	if message.StopReason != "" {
		events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: StopReasonOf(message.StopReason)})
	}
	if usage := UsageOf(message.Usage); usage != nil {
		events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: usage})
	}
	return events, nil
}

// metadataUserID is the user Messages' metadata.user_id names: a user_id the
// client's own metadata object carries (a Claude-shaped identity sent through
// Chat, as the channel probe does), else the request's user. Other metadata
// keys have no place in Messages' metadata and are left out.
func metadataUserID(metadata textgen.MetadataOptions) string {
	if len(metadata.Metadata) > 0 {
		var object map[string]json.RawMessage
		if json.Unmarshal(metadata.Metadata, &object) == nil {
			var userID string
			if json.Unmarshal(object["user_id"], &userID) == nil && userID != "" {
				return userID
			}
		}
	}
	return metadata.User
}
