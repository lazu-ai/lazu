package gemini

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// StreamEmitter writes a streamGenerateContent stream from content events
// for a Gemini client: one chunk per run of text or thought, a function
// call whole once it closes (Gemini never streams a call's arguments), the
// finish reason and usage on the last chunk.
type StreamEmitter struct {
	terminal bool
	calls    map[int]*call // open calls by ToolIndex, in start order
	order    []int
	col      textgen.Ending
}

type call struct {
	id, name, signature string
	args                strings.Builder
}

// NewStreamEmitter makes the emitter for one reply.
func NewStreamEmitter() *StreamEmitter {
	return &StreamEmitter{calls: map[int]*call{}}
}

func chunk(parts []dto.GeminiPart, finish string, usage *dto.GeminiUsageMetadata) *dto.GeminiChatResponse {
	candidate := dto.GeminiChatCandidate{Content: dto.GeminiChatContent{Role: "model", Parts: parts}, SafetyRatings: []dto.GeminiChatSafetyRating{}}
	if candidate.Content.Parts == nil {
		candidate.Content.Parts = []dto.GeminiPart{}
	}
	if finish != "" {
		candidate.FinishReason = &finish
	}
	response := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{candidate}}
	if usage != nil {
		response.UsageMetadata = *usage
	}
	return response
}

// Emit is the chunks for the content events given, in order.
func (e *StreamEmitter) Emit(events ...textgen.Event) []*dto.GeminiChatResponse {
	if e == nil || e.terminal {
		return nil
	}
	var out []*dto.GeminiChatResponse
	for _, event := range textgen.ForClient(events) {
		e.col.Add(event)
		switch event.Kind {
		case textgen.EventText, textgen.EventRefusal:
			if event.Text != "" {
				out = append(out, chunk([]dto.GeminiPart{{Text: event.Text}}, "", nil))
			}
		case textgen.EventReasoning:
			if event.Text != "" {
				out = append(out, chunk([]dto.GeminiPart{{Text: event.Text, Thought: true}}, "", nil))
			}
		case textgen.EventReasoningSignature:
			if event.SealedBy == SealedBy && event.Text != "" {
				out = append(out, chunk([]dto.GeminiPart{{Text: "", ThoughtSignature: event.Text}}, "", nil))
			}
		case textgen.EventToolStart:
			if _, open := e.calls[event.ToolIndex]; open {
				continue
			}
			c := &call{id: event.ToolCallID, name: event.ToolName}
			if event.SealedBy == SealedBy {
				c.signature = event.Text
			}
			e.calls[event.ToolIndex] = c
			e.order = append(e.order, event.ToolIndex)
		case textgen.EventToolArguments:
			if c, open := e.calls[event.ToolIndex]; open {
				c.args.WriteString(event.Text)
				if c.id == "" {
					c.id = event.ToolCallID
				}
				if c.name == "" {
					c.name = event.ToolName
				}
			}
		case textgen.EventToolDone:
			if c, open := e.calls[event.ToolIndex]; open {
				if c.args.Len() == 0 {
					c.args.WriteString(event.Text)
				}
				out = append(out, chunk([]dto.GeminiPart{callPart(c)}, "", nil))
				e.close(event.ToolIndex)
			}
		case textgen.EventError:
			e.terminal = true
			return out
		}
	}
	return out
}

func (e *StreamEmitter) close(index int) {
	delete(e.calls, index)
	for i, open := range e.order {
		if open == index {
			e.order = append(e.order[:i], e.order[i+1:]...)
			break
		}
	}
}

func callPart(c *call) dto.GeminiPart {
	return dto.GeminiPart{FunctionCall: &dto.FunctionCall{ID: c.id, FunctionName: c.name, Arguments: argsObject(c.args.String())}, ThoughtSignature: c.signature}
}

// argsObject is a call's arguments as Gemini's args object: the JSON
// object, or the text under "arguments" when it is not one.
func argsObject(arguments string) map[string]any {
	if strings.TrimSpace(arguments) == "" {
		return map[string]any{}
	}
	var object map[string]any
	if json.Unmarshal([]byte(arguments), &object) == nil && object != nil {
		return object
	}
	return map[string]any{"arguments": arguments}
}

// Finish closes the reply: calls still open written whole, the finish
// reason and usage on the last chunk. The usage given wins over the one
// streamed. A reply whose upstream never said how it ended was cut off:
// Finish refuses it and writes nothing.
func (e *StreamEmitter) Finish(usage *dto.Usage) ([]*dto.GeminiChatResponse, error) {
	if e == nil || e.terminal {
		return nil, nil
	}
	if usage != nil {
		e.col.Add(textgen.Event{Kind: textgen.EventUsage, Usage: usage})
	}
	result := e.col.Result()
	if !result.StopSeen {
		return nil, errors.New("stream ended without a stop reason")
	}
	e.terminal = true
	var parts []dto.GeminiPart
	for _, index := range e.order {
		parts = append(parts, callPart(e.calls[index]))
	}
	metadata := usageMetadataOf(result.Usage)
	return []*dto.GeminiChatResponse{chunk(parts, FinishReasonOf(result.Stop), &metadata)}, nil
}

// Fail ends the reply with Gemini's error object, exactly once.
func (e *StreamEmitter) Fail(semantic *textgen.SemanticError) map[string]any {
	if e == nil || e.terminal {
		return nil
	}
	e.terminal = true
	message := "upstream stream failed"
	if semantic != nil && semantic.Message != "" {
		message = semantic.Message
	}
	return map[string]any{"error": map[string]any{"code": 502, "message": message, "status": "UNAVAILABLE"}}
}

// Result is how the reply ended so far, without its content: the
// emitter keeps none (textgen.Ending).
func (e *StreamEmitter) Result() textgen.Result { return e.col.Result() }

// usageMetadataOf is the shared usage fact as Gemini's usage metadata:
// thoughts apart from the candidates, as Gemini counts them.
func usageMetadataOf(usage *dto.Usage) dto.GeminiUsageMetadata {
	if usage == nil {
		return dto.GeminiUsageMetadata{}
	}
	thoughts := usage.CompletionTokenDetails.ReasoningTokens
	candidates := usage.CompletionTokens - thoughts
	if candidates < 0 {
		candidates = 0
	}
	return dto.GeminiUsageMetadata{
		PromptTokenCount:        usage.PromptTokens,
		CandidatesTokenCount:    candidates,
		ThoughtsTokenCount:      thoughts,
		CachedContentTokenCount: usage.PromptTokensDetails.CachedTokens,
		TotalTokenCount:         usage.PromptTokens + usage.CompletionTokens,
	}
}

// Render is the whole reply as a generateContent response, for a
// non-streaming Gemini client.
func Render(result textgen.Result) *dto.GeminiChatResponse {
	var parts []dto.GeminiPart
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			parts = append(parts, dto.GeminiPart{Text: text.String()})
			text.Reset()
		}
	}
	for _, part := range result.Parts {
		switch part.Kind {
		case textgen.PartText, textgen.PartRefusal:
			text.WriteString(part.Text)
		case textgen.PartReasoning:
			if part.Text != "" {
				flush()
				parts = append(parts, dto.GeminiPart{Text: part.Text, Thought: true})
			}
			if part.SealedBy == SealedBy && part.Signature != "" {
				flush()
				parts = append(parts, dto.GeminiPart{Text: "", ThoughtSignature: part.Signature})
			}
		case textgen.PartTool:
			flush()
			signature := ""
			if part.SealedBy == SealedBy {
				signature = part.Signature
			}
			parts = append(parts, dto.GeminiPart{FunctionCall: &dto.FunctionCall{ID: part.CallID, FunctionName: part.Name, Arguments: argsObject(part.Text)}, ThoughtSignature: signature})
		}
	}
	flush()
	metadata := usageMetadataOf(result.Usage)
	return chunk(parts, FinishReasonOf(result.Stop), &metadata)
}
