package anthropic_messages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// knownTopLevel are the Messages request fields the IR models. Anything else
// (mcp_servers, container, inference_geo, ...) is an unknown field: kept for
// a native relay, refused by a bridge.
var knownTopLevel = map[string]struct{}{
	"model": {}, "messages": {}, "system": {}, "max_tokens": {}, "stop_sequences": {},
	"temperature": {}, "top_p": {}, "top_k": {}, "stream": {}, "tools": {}, "tool_choice": {},
	"thinking": {}, "output_config": {}, "metadata": {}, "service_tier": {}, "speed": {},
	"cache_control": {}, "context_management": {},
}

// Decode is a Messages request (a Claude client's body) as the IR, beside
// the wire DTO the native relay sends. What the IR models it carries; what it
// does not is recorded as an unknown field at its path, so a native relay
// passes it and a bridge refuses it by name. Two things are carried as
// advisory and never refused: cache_control markers (they change cost, not
// output) and thinking display (how the client shows thinking). metadata
// is kept whole (see metadata).
func Decode(body []byte) (*textgen.Request, *dto.ClaudeRequest, error) {
	var wire dto.ClaudeRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(wire.Model) == "" {
		return nil, nil, fmt.Errorf("model is required")
	}
	if len(wire.Messages) == 0 {
		return nil, nil, fmt.Errorf("messages is required")
	}
	raw, err := textgen.RetainTopLevelFields(body)
	if err != nil {
		return nil, nil, err
	}
	request := &textgen.Request{
		Model: textgen.ModelRef{Canonical: wire.Model},
		Raw:   textgen.RawRetention{Protocol: textgen.ProtocolAnthropic, TopLevel: raw, UnknownFields: map[string]json.RawMessage{}},
	}
	for key, value := range raw {
		if _, ok := knownTopLevel[key]; !ok {
			request.Raw.UnknownFields[key] = append(json.RawMessage(nil), value...)
		}
	}
	d := decoder{request: request}
	d.sampling(raw)
	d.system(raw["system"])
	if err := d.messages(raw["messages"]); err != nil {
		return nil, nil, err
	}
	if err := d.tools(raw["tools"]); err != nil {
		return nil, nil, err
	}
	d.toolChoice(raw["tool_choice"])
	d.thinking(raw["thinking"], raw["output_config"])
	d.metadata(raw["metadata"])
	d.contextManagement(raw["context_management"])
	request.Vendor = textgen.VendorOptions{CacheControl: present(raw["cache_control"]), Speed: present(raw["speed"])}
	if len(request.Raw.UnknownFields) == 0 {
		request.Raw.UnknownFields = nil
	}
	return request, &wire, nil
}

type decoder struct {
	request *textgen.Request
}

func (d *decoder) unknown(path string, value json.RawMessage) {
	d.request.Raw.UnknownFields[path] = append(json.RawMessage(nil), value...)
}

// present is the value, or nil when it is absent or JSON null.
func present(value json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return append(json.RawMessage(nil), trimmed...)
}

func (d *decoder) sampling(raw map[string]json.RawMessage) {
	r := d.request
	var stream bool
	if json.Unmarshal(raw["stream"], &stream) == nil {
		r.Lifecycle.Stream = stream
	}
	var maxTokens uint
	if json.Unmarshal(raw["max_tokens"], &maxTokens) == nil && maxTokens > 0 {
		r.Sampling.MaxOutput = textgen.Some(maxTokens)
	}
	var number float64
	if value := present(raw["temperature"]); value != nil && json.Unmarshal(value, &number) == nil {
		r.Sampling.Temperature = textgen.Some(number)
	}
	if value := present(raw["top_p"]); value != nil && json.Unmarshal(value, &number) == nil {
		r.Sampling.TopP = textgen.Some(number)
	}
	var topK int
	if value := present(raw["top_k"]); value != nil && json.Unmarshal(value, &topK) == nil {
		r.Sampling.TopK = textgen.Some(topK)
	}
	if value := present(raw["stop_sequences"]); value != nil {
		var stops []string
		if json.Unmarshal(value, &stops) == nil && len(stops) > 0 {
			encoded, _ := json.Marshal(stops)
			r.Sampling.Stop = textgen.Some(json.RawMessage(encoded))
		}
	}
	var tier string
	if json.Unmarshal(raw["service_tier"], &tier) == nil {
		r.Metadata.ServiceTier = tier
	}
}

// block is one content block, its known fields decoded and the rest kept
// for the unknown-field check.
type block struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	CacheControl json.RawMessage `json:"cache_control"`
	Source       *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
		URL       string `json:"url"`
	} `json:"source"`
	Title     string          `json:"title"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
}

// knownBlockFields are the keys each block type carries in the IR.
var knownBlockFields = map[string][]string{
	"text":              {"type", "text", "cache_control", "citations"},
	"image":             {"type", "source", "cache_control"},
	"document":          {"type", "source", "title", "cache_control"},
	"tool_use":          {"type", "id", "name", "input", "cache_control"},
	"tool_result":       {"type", "tool_use_id", "content", "is_error", "cache_control"},
	"thinking":          {"type", "thinking", "signature"},
	"redacted_thinking": {"type", "data"},
}

func (d *decoder) checkFields(path, kind string, value json.RawMessage) {
	allowed, ok := knownBlockFields[kind]
	if !ok {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil {
		return
	}
	for key, field := range fields {
		if present(field) == nil {
			continue
		}
		known := false
		for _, name := range allowed {
			if name == key {
				known = true
				break
			}
		}
		if !known || (key == "citations" && string(bytes.TrimSpace(field)) != "[]") {
			if known {
				// citations a text block carries back are Anthropic's to
				// interpret; another vendor has no place for them
				d.unknown(path+".citations", field)
				continue
			}
			d.unknown(path+"."+key, field)
		}
	}
}

func (d *decoder) system(value json.RawMessage) {
	value = present(value)
	if value == nil {
		return
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		if strings.TrimSpace(text) != "" {
			d.request.Instructions = append(d.request.Instructions, textgen.Instruction{Role: "system", Text: text})
		}
		return
	}
	var blocks []json.RawMessage
	if json.Unmarshal(value, &blocks) != nil {
		d.unknown("system", value)
		return
	}
	for index, raw := range blocks {
		path := fmt.Sprintf("system[%d]", index)
		var b block
		if json.Unmarshal(raw, &b) != nil || b.Type != "text" {
			d.unknown(path, raw)
			continue
		}
		d.checkFields(path, "text", raw)
		d.request.Instructions = append(d.request.Instructions, textgen.Instruction{Role: "system", Text: b.Text, CacheControl: present(b.CacheControl)})
	}
}

func (d *decoder) messages(value json.RawMessage) error {
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(value, &messages); err != nil {
		return fmt.Errorf("messages: %w", err)
	}
	for index, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "user", "assistant", "system":
		default:
			return fmt.Errorf("messages[%d].role %q is not user, assistant or system", index, message.Role)
		}
		var text string
		if json.Unmarshal(message.Content, &text) == nil {
			d.request.Conversation = append(d.request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: role, Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: text}}})
			continue
		}
		var blocks []json.RawMessage
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return fmt.Errorf("messages[%d].content: %w", index, err)
		}
		d.blocks(index, role, blocks)
	}
	return nil
}

// blocks is one message's content as IR items, in order: runs of text and
// media become a message, a tool call or result its own item, thinking a
// reasoning item, and the server-side blocks opaque items.
func (d *decoder) blocks(index int, role string, blocks []json.RawMessage) {
	var parts []textgen.ContentPart
	flush := func() {
		if len(parts) > 0 {
			d.request.Conversation = append(d.request.Conversation, textgen.InputItem{Kind: textgen.InputMessage, Role: role, Content: parts})
			parts = nil
		}
	}
	for blockIndex, raw := range blocks {
		path := fmt.Sprintf("messages[%d].content[%d]", index, blockIndex)
		var b block
		if err := json.Unmarshal(raw, &b); err != nil {
			d.unknown(path, raw)
			continue
		}
		d.checkFields(path, b.Type, raw)
		switch b.Type {
		case "text":
			parts = append(parts, textgen.ContentPart{Kind: textgen.ContentText, Text: b.Text, CacheControl: present(b.CacheControl)})
		case "image", "document":
			part, ok := d.media(path, b)
			if ok {
				parts = append(parts, part)
			}
		case "tool_use":
			flush()
			arguments := "{}"
			if input := present(b.Input); input != nil {
				var compact bytes.Buffer
				if json.Compact(&compact, input) == nil {
					arguments = compact.String()
				}
			}
			d.request.Conversation = append(d.request.Conversation, textgen.InputItem{Kind: textgen.InputFunctionCall, Role: "assistant", CallID: b.ID, FunctionName: b.Name, Arguments: arguments})
		case "tool_result":
			flush()
			d.toolResult(path, b)
		case "thinking", "redacted_thinking":
			flush()
			item := textgen.InputItem{Kind: textgen.InputReasoning, Role: "assistant", Opaque: []textgen.OpaqueValue{{Dialect: SealedBy, Value: append(json.RawMessage(nil), raw...)}}, WireType: b.Type}
			if b.Type == "thinking" && strings.TrimSpace(b.Thinking) != "" {
				item.PlainReasoning = textgen.Some(b.Thinking)
			}
			d.request.Conversation = append(d.request.Conversation, item)
		default:
			// server_tool_use, web_search_tool_result, container blocks:
			// Anthropic ran them; nothing else can replay them
			flush()
			d.request.Conversation = append(d.request.Conversation, textgen.InputItem{Kind: textgen.InputOpaque, Role: role, WireType: b.Type, Opaque: []textgen.OpaqueValue{{Dialect: SealedBy, Value: append(json.RawMessage(nil), raw...)}}})
		}
	}
	flush()
}

// media is an image or document block as a content part. A base64 source
// becomes a data URL, a url source its URL; a file id has no counterpart.
func (d *decoder) media(path string, b block) (textgen.ContentPart, bool) {
	if b.Source == nil {
		d.unknown(path+".source", json.RawMessage("null"))
		return textgen.ContentPart{}, false
	}
	kind := textgen.ContentImage
	if b.Type == "document" {
		kind = textgen.ContentFile
	}
	part := textgen.ContentPart{Kind: kind, Filename: b.Title, CacheControl: present(b.CacheControl)}
	switch b.Source.Type {
	case "base64":
		url := "data:" + b.Source.MediaType + ";base64," + b.Source.Data
		if kind == textgen.ContentImage {
			part.URL = url
		} else {
			part.Data = url
			part.Format = b.Source.MediaType
		}
	case "url":
		part.URL = b.Source.URL
	default:
		source, _ := json.Marshal(b.Source)
		d.unknown(path+".source", source)
		return textgen.ContentPart{}, false
	}
	return part, true
}

// toolResult is a tool_result block as a function result: its text joined,
// its images kept beside it, its error flag carried.
func (d *decoder) toolResult(path string, b block) {
	item := textgen.InputItem{Kind: textgen.InputFunctionResult, Role: "tool", CallID: b.ToolUseID, IsError: b.IsError}
	content := present(b.Content)
	var text string
	switch {
	case content == nil:
	case json.Unmarshal(content, &text) == nil:
		item.Output = text
	default:
		var blocks []json.RawMessage
		if json.Unmarshal(content, &blocks) != nil {
			d.unknown(path+".content", content)
			break
		}
		var texts []string
		for index, raw := range blocks {
			inner := fmt.Sprintf("%s.content[%d]", path, index)
			var part block
			if json.Unmarshal(raw, &part) != nil {
				d.unknown(inner, raw)
				continue
			}
			d.checkFields(inner, part.Type, raw)
			switch part.Type {
			case "text":
				texts = append(texts, part.Text)
			case "image":
				if media, ok := d.media(inner, part); ok {
					item.Content = append(item.Content, media)
				}
			default:
				d.unknown(inner, raw)
			}
		}
		item.Output = strings.Join(texts, "\n")
	}
	d.request.Conversation = append(d.request.Conversation, item)
}

func (d *decoder) tools(value json.RawMessage) error {
	value = present(value)
	if value == nil {
		return nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(value, &tools); err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	for index, raw := range tools {
		path := fmt.Sprintf("tools[%d]", index)
		var tool struct {
			Type         string          `json:"type"`
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			InputSchema  json.RawMessage `json:"input_schema"`
			Strict       *bool           `json:"strict"`
			CacheControl json.RawMessage `json:"cache_control"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		switch {
		case tool.Type == "" || tool.Type == "custom":
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			for key, field := range fields {
				switch key {
				case "type", "name", "description", "input_schema", "strict", "cache_control":
				default:
					if present(field) != nil {
						d.unknown(path+"."+key, field)
					}
				}
			}
			definition := textgen.ToolDefinition{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: present(tool.InputSchema), CacheControl: present(tool.CacheControl)}
			if tool.Strict != nil {
				definition.Strict = textgen.Some(*tool.Strict)
			}
			d.request.Tools = append(d.request.Tools, definition)
		case strings.HasPrefix(tool.Type, "web_search_"):
			d.request.Tools = append(d.request.Tools, textgen.ToolDefinition{Type: "web_search", Name: tool.Name, Hosted: append(json.RawMessage(nil), raw...)})
		default:
			// bash_, text_editor_, computer_, code_execution_ and the like:
			// Anthropic-defined tools only Anthropic runs or knows
			d.request.Tools = append(d.request.Tools, textgen.ToolDefinition{Type: tool.Type, Name: tool.Name, Hosted: append(json.RawMessage(nil), raw...)})
		}
	}
	return nil
}

func (d *decoder) toolChoice(value json.RawMessage) {
	value = present(value)
	if value == nil {
		return
	}
	var choice struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
	}
	if json.Unmarshal(value, &choice) != nil {
		d.unknown("tool_choice", value)
		return
	}
	policy := &d.request.ToolPolicy
	switch choice.Type {
	case "auto":
		policy.Mode = "auto"
	case "any":
		policy.Mode = "required"
	case "none":
		policy.Mode = "none"
	case "tool":
		policy.Mode = "function"
		policy.FunctionName = choice.Name
	default:
		d.unknown("tool_choice.type", value)
	}
	if choice.DisableParallelToolUse != nil {
		policy.ParallelToolCalls = textgen.Some(!*choice.DisableParallelToolUse)
	}
}

// thinking is Messages' thinking and output_config as the IR's reasoning and
// output: a budget is the budget, the effort (output_config.effort, the
// adaptive form's ask) the effort, disabled is effort none. display is how
// the client shows thinking and asks nothing of the model.
func (d *decoder) thinking(thinking, outputConfig json.RawMessage) {
	r := d.request
	var config map[string]json.RawMessage
	if value := present(outputConfig); value != nil {
		if json.Unmarshal(value, &config) != nil {
			d.unknown("output_config", value)
		}
	}
	for key, value := range config {
		switch key {
		case "effort":
			var effort string
			if json.Unmarshal(value, &effort) == nil {
				r.Reasoning.Effort = effort
			}
		case "format":
			var format struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			}
			if json.Unmarshal(value, &format) == nil && format.Type == "json_schema" && present(format.Schema) != nil {
				r.Output = textgen.OutputConstraint{Type: "json_schema", Name: "response", Schema: present(format.Schema)}
			} else {
				d.unknown("output_config.format", value)
			}
		default:
			d.unknown("output_config."+key, value)
		}
	}
	value := present(thinking)
	if value == nil {
		return
	}
	var t struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	}
	if json.Unmarshal(value, &t) != nil {
		d.unknown("thinking", value)
		return
	}
	switch t.Type {
	case "enabled":
		if t.BudgetTokens > 0 {
			r.Reasoning.Budget = textgen.Some(t.BudgetTokens)
		}
	case "adaptive":
		// the effort, when the client named one, is output_config's
	case "disabled":
		r.Reasoning.Effort = "none"
		r.Reasoning.Budget = textgen.Option[int]{}
	default:
		d.unknown("thinking.type", value)
	}
}

// metadata keeps the client's metadata object whole, as the legacy
// conversion forwarded it: agent sessions and routing hints read Claude
// Code's session from its user_id (middleware/agent_session.go), and a Chat
// or Responses upstream gets it as metadata, as before. It is not made the
// OpenAI user: Claude Code's user_id is a JSON document, not a user name.
func (d *decoder) metadata(value json.RawMessage) {
	value = present(value)
	if value == nil {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &fields) != nil {
		d.unknown("metadata", value)
		return
	}
	d.request.Metadata.Metadata = append(json.RawMessage(nil), value...)
}

// contextManagement keeps Anthropic's server-side context edits. Clearing
// prior thinking is the only kind another vendor needs no part in: thinking
// is never replayed to it. Any other edit changes what the model sees and
// only Anthropic can make it.
func (d *decoder) contextManagement(value json.RawMessage) {
	value = present(value)
	if value == nil {
		return
	}
	var management struct {
		Edits []struct {
			Type string `json:"type"`
		} `json:"edits"`
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(value, &management) != nil || json.Unmarshal(value, &fields) != nil {
		d.unknown("context_management", value)
		return
	}
	for key := range fields {
		if key != "edits" {
			d.unknown("context_management", value)
			return
		}
	}
	for _, edit := range management.Edits {
		if !strings.HasPrefix(edit.Type, "clear_thinking_") {
			d.unknown("context_management", value)
			return
		}
	}
}
