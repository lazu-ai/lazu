package anthropic_messages

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Frame is one SSE event of the Messages stream: its event name and the
// data object.
type Frame struct {
	Event string
	Data  map[string]any
}

type blockKind int

const (
	blockNone blockKind = iota
	blockText
	blockThinking
	blockTool
)

// StreamEmitter writes a Messages stream from content events for a Claude
// client: message_start, one content block per run of text, thinking or a
// tool call, message_delta with the stop reason and output usage,
// message_stop. It keeps a Collector so the stop reason and usage it closes
// with are the reply's.
type StreamEmitter struct {
	id       string
	model    string
	started  bool
	terminal bool
	index    int
	open     blockKind
	args     bool // the open tool block has had arguments written
	fresh    bool // a reasoning boundary: the next thinking is a block of its own
	// openTool is the upstream ToolIndex of the open tool block. Messages
	// blocks are sequential, while Chat and Responses interleave parallel
	// calls; a call that starts while another is open is held in pending
	// (start order) with its argument fragments until the open one closes.
	openTool int
	pending  []*pendingTool
	col      textgen.Ending
}

// pendingTool is a call held back while another tool block is open.
type pendingTool struct {
	index    int
	id, name string
	args     strings.Builder
	done     bool
}

func (e *StreamEmitter) pendingAt(index int) *pendingTool {
	for _, tool := range e.pending {
		if tool.index == index {
			return tool
		}
	}
	return nil
}

func toolUseID(id string, index int) string {
	if id == "" {
		return "toolu_lazu_" + itoa(index)
	}
	return id
}

// openTool opens a tool_use block for the call and writes the arguments
// held for it so far.
func (e *StreamEmitter) openToolBlock(index int, id, name, args string) []Frame {
	out := e.openBlock(blockTool, map[string]any{"id": toolUseID(id, index), "name": name, "input": map[string]any{}})
	e.openTool = index
	if args != "" {
		e.args = true
		out = append(out, e.delta(map[string]any{"type": "input_json_delta", "partial_json": args}))
	}
	return out
}

// openNamed opens the first held call once it is named, when no tool block
// is open: the call that was waiting only for its name streams on.
func (e *StreamEmitter) openNamed() []Frame {
	if e.open == blockTool || len(e.pending) == 0 || e.pending[0].name == "" {
		return nil
	}
	next := e.pending[0]
	e.pending = e.pending[1:]
	out := e.openToolBlock(next.index, next.id, next.name, next.args.String())
	if next.done {
		out = append(out, e.closeTool()...)
	}
	return out
}

// closeTool closes the open tool block and moves on through the held
// calls: each one already done is written whole; the first one still
// streaming is opened and streams on.
func (e *StreamEmitter) closeTool() []Frame {
	out := e.closeBlock()
	for len(e.pending) > 0 {
		next := e.pending[0]
		e.pending = e.pending[1:]
		out = append(out, e.openToolBlock(next.index, next.id, next.name, next.args.String())...)
		if !next.done {
			return out
		}
		out = append(out, e.closeBlock()...)
	}
	return out
}

// flushTools closes the open tool block and writes every held call whole,
// before a block of another kind or the end of the reply.
func (e *StreamEmitter) flushTools() []Frame {
	if e.open != blockTool && len(e.pending) == 0 {
		return nil
	}
	for i := range e.pending {
		e.pending[i].done = true
	}
	return e.closeTool()
}

// NewStreamEmitter makes the emitter for one reply; id is used until an
// EventStart names the upstream's.
func NewStreamEmitter(id, model string) *StreamEmitter {
	return &StreamEmitter{id: MessageID(id), model: model}
}

// MessageID is a reply's id as the Messages API gives one, msg_…; another
// API's chatcmpl-… or resp_… reads as one.
func MessageID(id string) string {
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return "msg_lazu"
	case strings.HasPrefix(id, "msg_"):
		return id
	}
	id = strings.TrimPrefix(id, "chatcmpl-")
	id = strings.TrimPrefix(id, "resp_")
	return "msg_" + id
}

func (e *StreamEmitter) frame(event string, data map[string]any) Frame {
	data["type"] = event
	return Frame{Event: event, Data: data}
}

func (e *StreamEmitter) start() []Frame {
	if e.started {
		return nil
	}
	e.started = true
	usage := usageTo(e.col.Result().Usage)
	usage["output_tokens"] = 0
	return []Frame{e.frame("message_start", map[string]any{"message": map[string]any{
		"id": e.id, "type": "message", "role": "assistant", "model": e.model, "content": []any{},
		"stop_reason": nil, "stop_sequence": nil, "usage": usage,
	}})}
}

func (e *StreamEmitter) closeBlock() []Frame {
	if e.open == blockNone {
		return nil
	}
	var out []Frame
	if e.open == blockTool && !e.args {
		out = append(out, e.frame("content_block_delta", map[string]any{"index": e.index, "delta": map[string]any{"type": "input_json_delta", "partial_json": "{}"}}))
	}
	out = append(out, e.frame("content_block_stop", map[string]any{"index": e.index}))
	e.index++
	e.open = blockNone
	return out
}

func (e *StreamEmitter) openBlock(kind blockKind, block map[string]any) []Frame {
	if e.open == kind && kind != blockTool {
		return nil
	}
	var out []Frame
	if kind != blockTool {
		out = e.flushTools()
	}
	out = append(out, e.closeBlock()...)
	e.args, e.fresh = false, false
	block["type"] = map[blockKind]string{blockText: "text", blockThinking: "thinking", blockTool: "tool_use"}[kind]
	e.open = kind
	return append(out, e.frame("content_block_start", map[string]any{"index": e.index, "content_block": block}))
}

func (e *StreamEmitter) delta(delta map[string]any) Frame {
	return e.frame("content_block_delta", map[string]any{"index": e.index, "delta": delta})
}

// Emit is the frames for the content events given, in order.
func (e *StreamEmitter) Emit(events ...textgen.Event) []Frame {
	if e == nil || e.terminal {
		return nil
	}
	var out []Frame
	for _, event := range events {
		if event.Kind == textgen.EventStart && !e.started {
			if event.ResponseID != "" {
				e.id = MessageID(event.ResponseID)
			}
			if event.Model != "" {
				e.model = event.Model
			}
		}
		if event.Kind == textgen.EventUsage || event.Kind == textgen.EventStart {
			// usage before the start goes into message_start's input count
			e.col.Add(event)
			if event.Kind == textgen.EventStart {
				out = append(out, e.start()...)
			}
			continue
		}
		out = append(out, e.start()...)
		switch event.Kind {
		case textgen.EventText, textgen.EventRefusal:
			// Messages has no refusal delta; a refusal is text the model
			// said, and its stop reason says why
			if event.Text == "" {
				continue
			}
			out = append(out, e.openBlock(blockText, map[string]any{"text": ""})...)
			out = append(out, e.delta(map[string]any{"type": "text_delta", "text": event.Text}))
		case textgen.EventReasoningStart:
			e.fresh = true
		case textgen.EventReasoning:
			if event.Text == "" {
				continue
			}
			if e.fresh {
				out = append(out, e.flushTools()...)
				out = append(out, e.closeBlock()...)
			}
			out = append(out, e.openBlock(blockThinking, map[string]any{"thinking": ""})...)
			out = append(out, e.delta(map[string]any{"type": "thinking_delta", "thinking": event.Text}))
		case textgen.EventReasoningSignature:
			if event.SealedBy != SealedBy || event.Text == "" {
				continue
			}
			if e.fresh {
				out = append(out, e.flushTools()...)
				out = append(out, e.closeBlock()...)
			}
			// a block signed with no text in it is a block all the same
			out = append(out, e.openBlock(blockThinking, map[string]any{"thinking": ""})...)
			out = append(out, e.delta(map[string]any{"type": "signature_delta", "signature": event.Text}))
		case textgen.EventReasoningSealed:
			if event.SealedBy != SealedBy || len(event.Sealed) == 0 {
				continue
			}
			out = append(out, e.flushTools()...)
			out = append(out, e.closeBlock()...)
			e.fresh = false
			var block map[string]any
			if json.Unmarshal(event.Sealed, &block) != nil {
				continue
			}
			out = append(out,
				e.frame("content_block_start", map[string]any{"index": e.index, "content_block": block}),
				e.frame("content_block_stop", map[string]any{"index": e.index}),
			)
			e.index++
		case textgen.EventToolStart:
			if e.open == blockTool && e.openTool == event.ToolIndex {
				break // the open call restated
			}
			if e.pendingAt(event.ToolIndex) != nil {
				break
			}
			if e.open == blockTool || event.ToolName == "" {
				// another call is open, or this one is not named yet (a Chat
				// upstream may name it on a later fragment): held, since a
				// tool_use block's id and name are written once, at its start
				e.pending = append(e.pending, &pendingTool{index: event.ToolIndex, id: event.ToolCallID, name: event.ToolName})
				break
			}
			out = append(out, e.openToolBlock(event.ToolIndex, event.ToolCallID, event.ToolName, "")...)
		case textgen.EventToolArguments:
			if e.open == blockTool && e.openTool == event.ToolIndex {
				if event.Text != "" {
					e.args = true
					out = append(out, e.delta(map[string]any{"type": "input_json_delta", "partial_json": event.Text}))
				}
			} else if held := e.pendingAt(event.ToolIndex); held != nil {
				held.args.WriteString(event.Text)
				if held.id == "" {
					held.id = event.ToolCallID
				}
				if held.name == "" {
					held.name = event.ToolName
				}
				out = append(out, e.openNamed()...)
			}
		case textgen.EventToolDone:
			if e.open == blockTool && e.openTool == event.ToolIndex {
				if event.Text != "" && !e.args {
					e.args = true
					out = append(out, e.delta(map[string]any{"type": "input_json_delta", "partial_json": event.Text}))
				}
				out = append(out, e.closeTool()...)
			} else if held := e.pendingAt(event.ToolIndex); held != nil {
				if event.Text != "" && held.args.Len() == 0 {
					held.args.WriteString(event.Text)
				}
				held.done = true
				if e.open != blockTool {
					// nothing streams before it: the held calls go out now
					out = append(out, e.closeTool()...)
				}
			}
		case textgen.EventStop:
		case textgen.EventError:
			e.col.Add(event)
			return append(out, e.fail(event.Error)...)
		}
		e.col.Add(event)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

// Finish closes the reply: the open block, message_delta with the stop
// reason and output usage, message_stop. The usage given wins over the one
// streamed. A reply whose upstream never said how it ended was cut off:
// Finish refuses it, writes nothing, and the caller ends the stream with
// Fail.
func (e *StreamEmitter) Finish(usage *dto.Usage) ([]Frame, error) {
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
	out := e.start()
	out = append(out, e.flushTools()...)
	out = append(out, e.closeBlock()...)
	e.terminal = true
	return append(out,
		e.frame("message_delta", map[string]any{"delta": map[string]any{"stop_reason": StopReasonTo(result.Stop), "stop_sequence": nil}, "usage": usageTo(result.Usage)}),
		e.frame("message_stop", map[string]any{}),
	), nil
}

// Fail ends the reply with Messages' error event, exactly once.
func (e *StreamEmitter) Fail(err error) []Frame {
	message := "stream failed"
	if err != nil {
		message = err.Error()
	}
	return e.fail(&textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: message})
}

func (e *StreamEmitter) fail(semantic *textgen.SemanticError) []Frame {
	if e == nil || e.terminal {
		return nil
	}
	out := e.start()
	out = append(out, e.flushTools()...)
	out = append(out, e.closeBlock()...)
	e.terminal = true
	if semantic == nil {
		semantic = &textgen.SemanticError{Type: "api_error", Message: "upstream stream failed"}
	}
	errType := semantic.Type
	if errType == "" {
		errType = "api_error"
	}
	return append(out, e.frame("error", map[string]any{"error": map[string]any{"type": errType, "message": semantic.Message}}))
}

// Result is how the reply ended so far, without its content: the
// emitter keeps none (textgen.Ending).
func (e *StreamEmitter) Result() textgen.Result { return e.col.Result() }

// usageTo is the shared usage fact as Messages' usage object.
func usageTo(usage *dto.Usage) map[string]any {
	out := map[string]any{"input_tokens": 0, "output_tokens": 0, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
	if usage == nil {
		return out
	}
	// Messages' input_tokens is the fresh input beside its cache buckets; an
	// OpenAI-style count includes them
	input := usage.PromptTokens
	if !usage.PromptExcludesCache {
		input -= usage.PromptTokensDetails.CachedTokens + usage.PromptTokensDetails.CachedCreationTokens
		if input < 0 {
			input = 0
		}
	}
	out["input_tokens"] = input
	out["output_tokens"] = usage.CompletionTokens
	out["cache_read_input_tokens"] = usage.PromptTokensDetails.CachedTokens
	out["cache_creation_input_tokens"] = usage.PromptTokensDetails.CachedCreationTokens
	if usage.PromptTokensDetails.CachedCreation5mTokens > 0 || usage.PromptTokensDetails.CachedCreation1hTokens > 0 {
		out["cache_creation"] = map[string]any{"ephemeral_5m_input_tokens": usage.PromptTokensDetails.CachedCreation5mTokens, "ephemeral_1h_input_tokens": usage.PromptTokensDetails.CachedCreation1hTokens}
	}
	if usage.CompletionTokenDetails.ReasoningTokens > 0 {
		out["output_tokens_details"] = map[string]any{"thinking_tokens": usage.CompletionTokenDetails.ReasoningTokens}
	}
	return out
}

// Render is the whole reply as a Messages response object, for a
// non-streaming Claude client.
func Render(result textgen.Result, model string) map[string]any {
	if result.Model != "" {
		model = result.Model
	}
	content := []any{}
	for _, part := range result.Parts {
		switch part.Kind {
		case textgen.PartText, textgen.PartRefusal:
			content = append(content, map[string]any{"type": "text", "text": part.Text})
		case textgen.PartReasoning:
			if part.Signature != "" && part.SealedBy != SealedBy {
				continue
			}
			if part.Text == "" && part.Signature == "" {
				continue
			}
			block := map[string]any{"type": "thinking", "thinking": part.Text}
			if part.Signature != "" {
				block["signature"] = part.Signature
			}
			content = append(content, block)
		case textgen.PartSealed:
			if part.SealedBy != SealedBy {
				continue
			}
			var block map[string]any
			if json.Unmarshal(part.Sealed, &block) == nil {
				content = append(content, block)
			}
		case textgen.PartTool:
			var input any = map[string]any{}
			if strings.TrimSpace(part.Text) != "" {
				var parsed any
				if json.Unmarshal([]byte(part.Text), &parsed) == nil {
					input = parsed
				}
			}
			content = append(content, map[string]any{"type": "tool_use", "id": toolUseID(part.CallID, part.ToolIndex), "name": part.Name, "input": input})
		}
	}
	return map[string]any{
		"id": MessageID(result.ResponseID), "type": "message", "role": "assistant", "model": model, "content": content,
		"stop_reason": StopReasonTo(result.Stop), "stop_sequence": nil, "usage": usageTo(result.Usage),
	}
}
