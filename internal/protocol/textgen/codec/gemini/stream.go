package gemini

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/tidwall/gjson"
)

// StreamDecoder turns a streamGenerateContent stream, chunk by chunk, into
// content events. Gemini restates nothing: each chunk is new parts, the
// last one the finish reason and the usage.
type StreamDecoder struct {
	started  bool
	thinking bool // a run of thought parts is open
	tools    int  // calls opened so far: their ToolIndex
	called   bool
}

// Decode is the events of one SSE data payload.
func (d *StreamDecoder) Decode(data string) ([]textgen.Event, error) {
	if d == nil {
		return nil, nil
	}
	if strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
		return nil, nil
	}
	if failure := gjson.Get(data, "error"); failure.Exists() {
		return []textgen.Event{{Kind: textgen.EventError, Error: errorOf(failure)}}, nil
	}
	var response dto.GeminiChatResponse
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return nil, err
	}
	var events []textgen.Event
	if !d.started {
		d.started = true
		events = append(events, textgen.Event{Kind: textgen.EventStart, ResponseID: gjson.Get(data, "responseId").String(), Model: gjson.Get(data, "modelVersion").String()}, textgen.Event{Kind: textgen.EventTurn, Role: "assistant"})
	}
	events = append(events, d.response(&response)...)
	return events, nil
}

func (d *StreamDecoder) response(response *dto.GeminiChatResponse) []textgen.Event {
	var events []textgen.Event
	if feedback := response.PromptFeedback; feedback != nil && feedback.BlockReason != nil && *feedback.BlockReason != "" && len(response.Candidates) == 0 {
		// the prompt was blocked: no candidate, the reason is a filter
		events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: textgen.StopFilter})
	}
	for _, candidate := range response.Candidates {
		if candidate.Index != 0 {
			continue // a bridge asks for one candidate
		}
		for _, part := range candidate.Content.Parts {
			events = append(events, d.part(part)...)
		}
		if candidate.FinishReason != nil && *candidate.FinishReason != "" {
			stop := StopReasonOf(*candidate.FinishReason)
			if stop == textgen.StopEnd && d.called {
				stop = textgen.StopTool
			}
			events = append(events, textgen.Event{Kind: textgen.EventStop, Stop: stop})
		}
	}
	if HasUsage(response.UsageMetadata) && (response.UsageMetadata.PromptTokenCount > 0 || response.UsageMetadata.CandidatesTokenCount > 0 || response.UsageMetadata.TotalTokenCount > 0) {
		usage := UsageOf(response.UsageMetadata, 0)
		events = append(events, textgen.Event{Kind: textgen.EventUsage, Usage: &usage})
	}
	return events
}

func (d *StreamDecoder) part(part dto.GeminiPart) []textgen.Event {
	var events []textgen.Event
	signed := func() {
		if part.ThoughtSignature != "" {
			events = append(events, textgen.Event{Kind: textgen.EventReasoningSignature, Text: part.ThoughtSignature, SealedBy: SealedBy})
		}
	}
	switch {
	case part.FunctionCall != nil:
		d.thinking = false
		index := d.tools
		d.tools++
		d.called = true
		id := part.FunctionCall.ID
		if id == "" {
			// Gemini often names no id; a Chat or Responses client pairs
			// the call with its result by one, and refuses a call without
			id = syntheticCallID()
		}
		start := textgen.Event{Kind: textgen.EventToolStart, ToolIndex: index, ToolCallID: id, ToolName: part.FunctionCall.FunctionName}
		if part.ThoughtSignature != "" {
			start.Text, start.SealedBy = part.ThoughtSignature, SealedBy
		}
		events = append(events, start, textgen.Event{Kind: textgen.EventToolDone, ToolIndex: index, ToolCallID: id, ToolName: part.FunctionCall.FunctionName, Text: responseArgumentsOf(part.FunctionCall.Arguments)})
	case part.Thought:
		if !d.thinking {
			d.thinking = true
			events = append(events, textgen.Event{Kind: textgen.EventReasoningStart})
		}
		if part.Text != "" {
			events = append(events, textgen.Event{Kind: textgen.EventReasoning, Text: part.Text})
		}
		signed()
	default:
		d.thinking = false
		if text := partText(part); text != "" {
			events = append(events, textgen.Event{Kind: textgen.EventText, Text: text})
		}
		signed()
	}
	return events
}

// partText is a part's text as a client reads it: an image or other media
// as a data-URL link, executed code and its output as fenced blocks, as the
// Chat conversion has always shown them.
func partText(part dto.GeminiPart) string {
	switch {
	case part.InlineData != nil:
		if strings.HasPrefix(part.InlineData.MimeType, "image") {
			return "![image](data:" + part.InlineData.MimeType + ";base64," + part.InlineData.Data + ")"
		}
		return fmt.Sprintf("[media](data:%s;base64,%s)", part.InlineData.MimeType, part.InlineData.Data)
	case part.ExecutableCode != nil:
		return "```" + part.ExecutableCode.Language + "\n" + part.ExecutableCode.Code + "\n```\n"
	case part.CodeExecutionResult != nil:
		return "```output\n" + part.CodeExecutionResult.Output + "\n```\n"
	}
	return part.Text
}

// argumentsOf is an incoming Gemini client's already-decoded arguments as a
// JSON object string. It must not unescape strings a second time.
func argumentsOf(arguments any) string {
	return marshalArguments(arguments)
}

// responseArgumentsOf is an upstream Gemini response's arguments as a JSON
// object string. Some compatible providers double-escape these values, so the
// established response path normalizes them before serialization.
func responseArgumentsOf(arguments any) string {
	if object, ok := arguments.(map[string]any); ok {
		arguments = UnescapeArguments(object)
	}
	return marshalArguments(arguments)
}

func marshalArguments(arguments any) string {
	if arguments == nil {
		return "{}"
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func errorOf(failure gjson.Result) *textgen.SemanticError {
	semantic := &textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: "upstream stream failed"}
	if status := failure.Get("status").String(); status != "" {
		semantic.Type = strings.ToLower(status)
	}
	if code := failure.Get("code").String(); code != "" {
		semantic.Code = code
	}
	if message := failure.Get("message").String(); message != "" {
		semantic.Message = message
	}
	return semantic
}

// StopReasonOf is a Gemini finish reason in the IR's words.
func StopReasonOf(reason string) textgen.StopReason {
	switch strings.ToUpper(reason) {
	case "STOP":
		return textgen.StopEnd
	case "MAX_TOKENS":
		return textgen.StopLength
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "LANGUAGE":
		return textgen.StopFilter
	}
	return textgen.StopEnd
}

// FinishReasonOf is a stop reason in Gemini's words.
func FinishReasonOf(stop textgen.StopReason) string {
	switch stop {
	case textgen.StopLength:
		return "MAX_TOKENS"
	case textgen.StopFilter:
		return "SAFETY"
	}
	return "STOP"
}

// DecodeResponse is a whole generateContent reply as content events, in the
// order its stream would have said them.
func DecodeResponse(body []byte) ([]textgen.Event, error) {
	decoder := &StreamDecoder{}
	return decoder.Decode(string(body))
}

// syntheticCallID is an id for a function call Gemini sent without one,
// unique across turns as the client's history needs.
func syntheticCallID() string {
	var random [12]byte
	_, _ = rand.Read(random[:])
	return "call_" + hex.EncodeToString(random[:])
}
