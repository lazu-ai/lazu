package openai_chat

import (
	"errors"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// StreamEmitter writes Chat Completions chunks from content events for a
// Chat client: a role chunk first, then one chunk per delta, tool calls
// numbered as Chat numbers them, the finish reason and usage on the last
// chunk. The caller writes [DONE]. Reasoning signatures and sealed items
// have no Chat field and are left out.
type StreamEmitter struct {
	id        string
	model     string
	created   int64
	started   bool
	roleSent  bool
	terminal  bool
	tools     map[int]int // upstream ToolIndex -> Chat tool index
	toolCount int
	col       textgen.Collector
}

// NewStreamEmitter makes the emitter for one reply; id is used until an
// EventStart names the upstream's.
func NewStreamEmitter(id, model string) *StreamEmitter {
	return &StreamEmitter{id: CompletionID(id), model: model, created: time.Now().Unix(), tools: map[int]int{}}
}

// CompletionID is a reply's id as Chat gives one, chatcmpl-…
func CompletionID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "chatcmpl-") {
		return id
	}
	id = strings.TrimPrefix(id, "resp_")
	id = strings.TrimPrefix(id, "msg_")
	if id == "" {
		id = "lazu"
	}
	return "chatcmpl-" + id
}

func (e *StreamEmitter) chunk(delta dto.ChatCompletionsStreamResponseChoiceDelta, finish *string, usage *dto.Usage) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{Id: e.id, Object: "chat.completion.chunk", Created: e.created, Model: e.model, Choices: []dto.ChatCompletionsStreamResponseChoice{{Index: 0, Delta: delta, FinishReason: finish}}, Usage: usage}
}

func (e *StreamEmitter) role() []*dto.ChatCompletionsStreamResponse {
	if e.roleSent {
		return nil
	}
	e.roleSent = true
	empty := ""
	return []*dto.ChatCompletionsStreamResponse{e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant", Content: &empty}, nil, nil)}
}

// Emit is the chunks for the content events given, in order.
func (e *StreamEmitter) Emit(events ...textgen.Event) []*dto.ChatCompletionsStreamResponse {
	if e == nil || e.terminal {
		return nil
	}
	var out []*dto.ChatCompletionsStreamResponse
	for _, event := range events {
		switch event.Kind {
		case textgen.EventStart:
			if !e.started {
				e.started = true
				if event.ResponseID != "" {
					e.id = CompletionID(event.ResponseID)
				}
				if event.Model != "" {
					e.model = event.Model
				}
				if event.CreatedAt > 0 {
					e.created = event.CreatedAt
				}
			}
		case textgen.EventTurn:
			out = append(out, e.role()...)
		case textgen.EventText:
			if event.Text == "" {
				continue
			}
			out = append(out, e.role()...)
			text := event.Text
			out = append(out, e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{Content: &text}, nil, nil))
		case textgen.EventRefusal:
			if event.Text == "" {
				continue
			}
			out = append(out, e.role()...)
			refusal := event.Text
			out = append(out, e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{Refusal: &refusal}, nil, nil))
		case textgen.EventReasoning:
			if event.Text == "" {
				continue
			}
			out = append(out, e.role()...)
			reasoning := event.Text
			out = append(out, e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: &reasoning}, nil, nil))
		case textgen.EventToolStart:
			if _, open := e.tools[event.ToolIndex]; open {
				continue
			}
			out = append(out, e.role()...)
			index := e.toolCount
			e.toolCount++
			e.tools[event.ToolIndex] = index
			call := dto.ToolCallResponse{ID: event.ToolCallID, Type: "function", Function: dto.FunctionResponse{Name: event.ToolName, Arguments: ""}}
			call.SetIndex(index)
			out = append(out, e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{call}}, nil, nil))
		case textgen.EventToolArguments, textgen.EventToolDone:
			index, open := e.tools[event.ToolIndex]
			if !open {
				continue
			}
			text := event.Text
			if event.Kind == textgen.EventToolDone {
				// the upstream restated the whole arguments: only a call that
				// streamed none needs them
				if text == "" || e.argsSeen(event.ToolIndex) {
					e.col.Add(event)
					continue
				}
			}
			if text == "" && event.ToolCallID == "" && event.ToolName == "" {
				continue
			}
			call := dto.ToolCallResponse{ID: event.ToolCallID, Type: "function", Function: dto.FunctionResponse{Name: event.ToolName, Arguments: text}}
			call.SetIndex(index)
			out = append(out, e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{call}}, nil, nil))
		case textgen.EventError:
			e.col.Add(event)
			e.terminal = true
			message := "upstream stream failed"
			if event.Error != nil && event.Error.Message != "" {
				message = event.Error.Message
			}
			chunk := e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{}, nil, nil)
			chunk.Choices = nil
			chunk.Error = map[string]any{"type": errorType(event.Error), "message": message, "code": errorCode(event.Error)}
			return append(out, chunk)
		}
		e.col.Add(event)
	}
	return out
}

func (e *StreamEmitter) argsSeen(toolIndex int) bool {
	for _, part := range e.col.Result().Parts {
		if part.Kind == textgen.PartTool && part.ToolIndex == toolIndex {
			return part.Text != ""
		}
	}
	return false
}

// Finish closes the reply with the finish reason and usage (the usage given
// wins over the one streamed). A reply that said nothing still gets its
// role chunk. A reply whose upstream never said how it ended was cut off:
// Finish refuses it, writes nothing, and the caller ends the stream with
// Fail, as the Responses emitter does.
func (e *StreamEmitter) Finish(usage *dto.Usage) ([]*dto.ChatCompletionsStreamResponse, error) {
	if e == nil || e.terminal {
		return nil, nil
	}
	if usage != nil {
		e.col.Add(textgen.Event{Kind: textgen.EventUsage, Usage: usage})
	}
	result := e.col.Result()
	if !result.StopSeen {
		return nil, errStreamWithoutStop
	}
	e.terminal = true
	out := e.role()
	finish := FinishReasonOf(result.Stop)
	return append(out, e.chunk(dto.ChatCompletionsStreamResponseChoiceDelta{}, &finish, result.Usage)), nil
}

// errStreamWithoutStop is a stream that ended without its stop reason.
var errStreamWithoutStop = errors.New("stream ended without a stop reason")

// Result is the reply collected so far.
func (e *StreamEmitter) Result() textgen.Result { return e.col.Result() }

func errorType(err *textgen.SemanticError) string {
	if err == nil || err.Type == "" {
		return "api_error"
	}
	return err.Type
}

func errorCode(err *textgen.SemanticError) string {
	if err == nil || err.Code == "" {
		return "upstream_stream_failed"
	}
	return err.Code
}

// Render is the whole reply as a Chat completion object, for a
// non-streaming Chat client.
func Render(result textgen.Result, model string) *dto.OpenAITextResponse {
	if result.Model != "" {
		model = result.Model
	}
	created := result.CreatedAt
	if created == 0 {
		created = time.Now().Unix()
	}
	var text, refusal, reasoning strings.Builder
	var calls []dto.ToolCallRequest
	for _, part := range result.Parts {
		switch part.Kind {
		case textgen.PartText:
			text.WriteString(part.Text)
		case textgen.PartRefusal:
			refusal.WriteString(part.Text)
		case textgen.PartReasoning:
			reasoning.WriteString(part.Text)
		case textgen.PartTool:
			args := part.Text
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			calls = append(calls, dto.ToolCallRequest{ID: part.CallID, Type: "function", Function: dto.FunctionRequest{Name: part.Name, Arguments: args}})
		}
	}
	message := dto.Message{Role: "assistant", ReasoningContent: reasoning.String()}
	if refusal.Len() > 0 {
		// a refusal is typed, as Chat clients read it apart from an answer
		value := refusal.String()
		message.Refusal = &value
	}
	if text.Len() > 0 || refusal.Len() == 0 {
		message.SetStringContent(text.String())
	}
	if len(calls) > 0 {
		message.SetToolCalls(calls)
	}
	response := &dto.OpenAITextResponse{Id: CompletionID(result.ResponseID), Object: "chat.completion", Created: created, Model: model, Choices: []dto.OpenAITextResponseChoice{{Index: 0, Message: message, FinishReason: FinishReasonOf(result.Stop)}}}
	if result.Usage != nil {
		response.Usage = *result.Usage
	}
	return response
}
