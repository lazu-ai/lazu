package openai_responses

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

const (
	defaultChatStreamRetainedOutputBytes = 16 << 20
	minimumChatStreamRetainedOutputBytes = 64 << 10
	chatStreamBytesPerOutputToken        = 16
)

// SealedBy is the dialect name a Responses reasoning item is sealed under
// (textgen.Event.SealedBy): only a Responses client may be given it back.
const SealedBy = "openai_responses"

type itemKind int

const (
	itemMessage itemKind = iota
	itemReasoning
	itemTool
	itemRaw
)

// outputItem is one item of the reply as the Responses wire counts them,
// in output order.
type outputItem struct {
	kind   itemKind
	id     string
	index  int
	status string
	// message
	text, refusal             strings.Builder
	textAdded, refusalAdded   bool
	textContent, refusalIndex int
	nextContent               int
	// reasoning
	reasoning      strings.Builder
	reasoningAdded bool
	// tool
	toolIndex int
	callID    string
	name      string
	args      strings.Builder
	// raw: a sealed item passed through as the upstream wrote it
	raw json.RawMessage
}

// StreamEmitter is the Responses SSE state machine: content events in,
// Responses events out. It owns sequence numbers, item ids, output and
// content indexes, the content-part lifecycle, the retained output the
// terminal event must carry (bounded per request), and the envelope. One
// emitter serves one reply.
type StreamEmitter struct {
	sequence   uint64
	responseID string
	suffix     string
	model      string
	createdAt  int64
	request    *textgen.Request

	started  bool
	terminal bool
	turnSeen bool
	stopSeen bool
	stop     textgen.StopReason

	items       []*outputItem
	message     *outputItem
	reasoning   *outputItem
	reasonCount int
	tools       map[int]*outputItem

	usage               *dto.Usage
	retainedOutputBytes int
	retainedOutputLimit int
}

// NewStreamEmitter makes the emitter for one reply. responseID is the id
// the reply is answered under until an EventStart names the upstream's;
// request is the client's, whose configuration the envelope echoes.
func NewStreamEmitter(responseID, model string, request *textgen.Request, retainedOutputLimit ...int) *StreamEmitter {
	if responseID == "" {
		responseID = "resp_lazu"
	}
	limit := defaultChatStreamRetainedOutputBytes
	if len(retainedOutputLimit) > 0 && retainedOutputLimit[0] > 0 {
		limit = retainedOutputLimit[0]
	}
	e := &StreamEmitter{model: model, request: request, createdAt: time.Now().Unix(), tools: map[int]*outputItem{}, retainedOutputLimit: limit}
	e.setResponseID(responseID)
	return e
}

func (e *StreamEmitter) setResponseID(id string) {
	e.responseID = normalizeResponseID(id)
	e.suffix = stableSuffix(id)
}

// RetainedOutputLimit is how much reply text an emitter for request may
// retain for the terminal event: the request's output limit times a bytes
// per token allowance, within fixed bounds.
func RetainedOutputLimit(request *textgen.Request) int {
	if request == nil || !request.Sampling.MaxOutput.Set || request.Sampling.MaxOutput.Value == 0 {
		return defaultChatStreamRetainedOutputBytes
	}
	requested := uint64(request.Sampling.MaxOutput.Value)
	maxDynamicTokens := uint64((defaultChatStreamRetainedOutputBytes - minimumChatStreamRetainedOutputBytes) / chatStreamBytesPerOutputToken)
	if requested > maxDynamicTokens {
		return defaultChatStreamRetainedOutputBytes
	}
	return int(requested*chatStreamBytesPerOutputToken + minimumChatStreamRetainedOutputBytes)
}

// Emit is the Responses events for the content events given, in order. An
// error (retained output over its limit) leaves the emitter in a state Fail
// closes; nothing after the failing event is applied.
func (e *StreamEmitter) Emit(events ...textgen.Event) ([]map[string]any, error) {
	if e == nil || e.terminal {
		return nil, nil
	}
	var out []map[string]any
	for _, event := range events {
		if event.Kind == textgen.EventStart && !e.started {
			if event.ResponseID != "" {
				e.setResponseID(event.ResponseID)
			}
			if event.Model != "" {
				e.model = event.Model
			}
			if event.CreatedAt > 0 {
				e.createdAt = event.CreatedAt
			}
		}
		out = append(out, e.startEvents()...)
		switch event.Kind {
		case textgen.EventStart:
		case textgen.EventUsage:
			if event.Usage != nil {
				e.usage = event.Usage
			}
		case textgen.EventTurn:
			e.turnSeen = true
		case textgen.EventText, textgen.EventRefusal:
			if event.Text == "" {
				continue
			}
			if err := e.retainOutput(event.Text); err != nil {
				return out, err
			}
			refusal := event.Kind == textgen.EventRefusal
			opened, contentIndex := e.ensureMessage(refusal)
			out = append(out, opened...)
			if refusal {
				e.message.refusal.WriteString(event.Text)
				out = append(out, e.send("response.refusal.delta", map[string]any{"item_id": e.message.id, "output_index": e.message.index, "content_index": contentIndex, "delta": event.Text}))
			} else {
				e.message.text.WriteString(event.Text)
				out = append(out, e.send("response.output_text.delta", map[string]any{"item_id": e.message.id, "output_index": e.message.index, "content_index": contentIndex, "delta": event.Text}))
			}
		case textgen.EventReasoningStart:
			out = append(out, e.closeReasoning("completed")...)
		case textgen.EventReasoning:
			if event.Text == "" {
				continue
			}
			if err := e.retainOutput(event.Text); err != nil {
				return out, err
			}
			out = append(out, e.ensureReasoning()...)
			e.reasoning.reasoning.WriteString(event.Text)
			out = append(out, e.send("response.reasoning_text.delta", map[string]any{"item_id": e.reasoning.id, "output_index": e.reasoning.index, "content_index": 0, "delta": event.Text}))
		case textgen.EventReasoningSignature:
			// a signature belongs to the dialect that made it; Responses
			// has no field for another's
		case textgen.EventReasoningSealed:
			if event.SealedBy != SealedBy || len(event.Sealed) == 0 {
				continue
			}
			out = append(out, e.closeReasoning("completed")...)
			item := e.newItem(itemRaw, "")
			item.raw = append(json.RawMessage(nil), event.Sealed...)
			out = append(out, e.send("response.output_item.added", map[string]any{"output_index": item.index, "item": item.raw}))
			out = append(out, e.send("response.output_item.done", map[string]any{"output_index": item.index, "item": item.raw}))
		case textgen.EventToolStart:
			if _, open := e.tools[event.ToolIndex]; open {
				continue
			}
			item := e.newItem(itemTool, fmt.Sprintf("fc_%s_%d", e.suffix, event.ToolIndex))
			if event.ItemID != "" {
				item.id = event.ItemID
			}
			item.toolIndex, item.callID, item.name = event.ToolIndex, event.ToolCallID, event.ToolName
			e.tools[event.ToolIndex] = item
			out = append(out, e.send("response.output_item.added", map[string]any{"output_index": item.index, "item": map[string]any{"type": "function_call", "id": item.id, "call_id": item.callID, "name": item.name, "arguments": "", "status": "in_progress"}}))
		case textgen.EventToolArguments, textgen.EventToolDone:
			item := e.tools[event.ToolIndex]
			if item == nil {
				continue
			}
			if event.ToolCallID != "" {
				item.callID = event.ToolCallID
			}
			if event.ToolName != "" {
				item.name = event.ToolName
			}
			if event.Kind == textgen.EventToolDone {
				if event.Text != "" && item.args.Len() == 0 {
					if err := e.retainOutput(event.Text); err != nil {
						return out, err
					}
					item.args.WriteString(event.Text)
				}
				continue
			}
			if event.Text == "" {
				continue
			}
			if err := e.retainOutput(event.Text); err != nil {
				return out, err
			}
			item.args.WriteString(event.Text)
			out = append(out, e.send("response.function_call_arguments.delta", map[string]any{"item_id": item.id, "output_index": item.index, "delta": event.Text}))
		case textgen.EventStop:
			e.stopSeen, e.stop = true, event.Stop
		case textgen.EventError:
			semantic := event.Error
			if semantic == nil {
				semantic = &textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: "upstream stream failed"}
			}
			return append(out, e.FailWithSemanticError(semantic)...), nil
		}
	}
	return out, nil
}

// Finish closes the reply: every open item, then the terminal event, which
// carries the whole output and usage (the usage given wins over the one
// streamed). A reply that never said why it stopped is an error.
func (e *StreamEmitter) Finish(usage *dto.Usage) ([]map[string]any, error) {
	if e == nil || e.terminal {
		return nil, nil
	}
	if !e.stopSeen {
		return nil, fmt.Errorf("stream ended without a stop reason")
	}
	if usage != nil {
		e.usage = chatUsageToResponses(usage)
	}
	out := e.startEvents()
	status, eventType, reason := "completed", "response.completed", ""
	switch e.stop {
	case textgen.StopLength:
		status, eventType, reason = "incomplete", "response.incomplete", "max_output_tokens"
	case textgen.StopFilter:
		status, eventType, reason = "incomplete", "response.incomplete", "content_filter"
	}
	if e.turnSeen && e.message == nil && len(e.tools) == 0 {
		opened, _ := e.ensureMessage(false)
		out = append(out, opened...)
	}
	out = append(out, e.closeItems(status)...)
	response := e.envelope(status)
	if reason != "" {
		response["incomplete_details"] = map[string]any{"reason": reason}
	}
	e.terminal = true
	return append(out, e.send(eventType, map[string]any{"response": response})), nil
}

// Fail ends the reply with an error of lazu's own words.
func (e *StreamEmitter) Fail(err error, usage ...*dto.Usage) []map[string]any {
	message := "stream failed"
	if err != nil {
		message = err.Error()
	}
	return e.FailWithSemanticError(&textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: message}, usage...)
}

// FailWithSemanticError ends the reply with response.failed, exactly once,
// after closing what was open as incomplete. The payload's type is what a
// caller keys its settlement on.
func (e *StreamEmitter) FailWithSemanticError(streamError *textgen.SemanticError, usage ...*dto.Usage) []map[string]any {
	if e == nil || e.terminal {
		return nil
	}
	if len(usage) > 0 && usage[0] != nil {
		e.usage = chatUsageToResponses(usage[0])
	}
	out := e.startEvents()
	e.terminal = true
	if streamError == nil {
		streamError = &textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: "The model stopped the stream with an error. Check any partial output before retrying."}
	}
	out = append(out, e.closeItems("incomplete")...)
	response := e.envelope("failed")
	response["error"] = streamError
	return append(out, e.send("response.failed", map[string]any{"response": response}))
}

// VisitBillableOutput exposes the retained output for a token estimate
// without a second stream-sized buffer: each value is one semantic field.
// It returns how many function calls the reply made.
func (e *StreamEmitter) VisitBillableOutput(visit func(string)) int {
	if e == nil {
		return 0
	}
	toolCount := 0
	for _, item := range e.items {
		switch item.kind {
		case itemReasoning:
			if visit != nil && item.reasoning.Len() > 0 {
				visit(item.reasoning.String())
			}
		case itemMessage:
			if visit != nil {
				if item.text.Len() > 0 {
					visit(item.text.String())
				}
				if item.refusal.Len() > 0 {
					visit(item.refusal.String())
				}
			}
		case itemTool:
			toolCount++
			if visit != nil {
				if item.name != "" {
					visit(item.name)
				}
				if item.args.Len() > 0 {
					visit(item.args.String())
				}
			}
		}
	}
	return toolCount
}

func (e *StreamEmitter) retainOutput(value string) error {
	if value == "" {
		return nil
	}
	if e.retainedOutputLimit <= 0 {
		e.retainedOutputLimit = defaultChatStreamRetainedOutputBytes
	}
	if len(value) > e.retainedOutputLimit-e.retainedOutputBytes {
		return fmt.Errorf("bridged stream retained output exceeds %d-byte limit", e.retainedOutputLimit)
	}
	e.retainedOutputBytes += len(value)
	return nil
}

func (e *StreamEmitter) send(eventType string, fields map[string]any) map[string]any {
	fields["type"] = eventType
	fields["sequence_number"] = e.sequence
	e.sequence++
	return fields
}

func (e *StreamEmitter) startEvents() []map[string]any {
	if e.started {
		return nil
	}
	e.started = true
	return []map[string]any{
		e.send("response.created", map[string]any{"response": e.envelope("in_progress")}),
		e.send("response.in_progress", map[string]any{"response": e.envelope("in_progress")}),
	}
}

func (e *StreamEmitter) newItem(kind itemKind, id string) *outputItem {
	item := &outputItem{kind: kind, id: id, index: len(e.items)}
	e.items = append(e.items, item)
	return item
}

// ensureMessage opens the reply's message item and the text or refusal
// content part, as needed, and gives the part's content index.
func (e *StreamEmitter) ensureMessage(refusal bool) ([]map[string]any, int) {
	var out []map[string]any
	if e.message == nil {
		e.message = e.newItem(itemMessage, "msg_"+e.suffix)
		out = append(out, e.send("response.output_item.added", map[string]any{"output_index": e.message.index, "item": map[string]any{"type": "message", "id": e.message.id, "role": "assistant", "content": []any{}, "status": "in_progress"}}))
	}
	m := e.message
	added, contentIndex := m.textAdded, m.textContent
	if refusal {
		added, contentIndex = m.refusalAdded, m.refusalIndex
	}
	if !added {
		contentIndex = m.nextContent
		m.nextContent++
		part := map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		if refusal {
			m.refusalAdded, m.refusalIndex = true, contentIndex
			part = map[string]any{"type": "refusal", "refusal": ""}
		} else {
			m.textAdded, m.textContent = true, contentIndex
		}
		out = append(out, e.send("response.content_part.added", map[string]any{"item_id": m.id, "output_index": m.index, "content_index": contentIndex, "part": part}))
	}
	return out, contentIndex
}

// ensureReasoning opens a reasoning item and its one content part.
func (e *StreamEmitter) ensureReasoning() []map[string]any {
	if e.reasoning != nil {
		return nil
	}
	id := "rs_lazu_" + e.suffix
	if e.reasonCount > 0 {
		id = fmt.Sprintf("rs_lazu_%s_%d", e.suffix, e.reasonCount)
	}
	e.reasonCount++
	e.reasoning = e.newItem(itemReasoning, id)
	e.reasoning.reasoningAdded = true
	return []map[string]any{
		e.send("response.output_item.added", map[string]any{"output_index": e.reasoning.index, "item": dto.NewResponsesReasoningItem(e.reasoning.id, "in_progress", nil)}),
		e.send("response.content_part.added", map[string]any{"item_id": e.reasoning.id, "output_index": e.reasoning.index, "content_index": 0, "part": map[string]any{"type": "reasoning_text", "text": ""}}),
	}
}

// closeReasoning ends the open reasoning block, if any.
func (e *StreamEmitter) closeReasoning(status string) []map[string]any {
	item := e.reasoning
	if item == nil {
		return nil
	}
	e.reasoning = nil
	return e.closeItem(item, status)
}

func (e *StreamEmitter) closeItem(item *outputItem, status string) []map[string]any {
	item.status = status
	switch item.kind {
	case itemReasoning:
		text := item.reasoning.String()
		return []map[string]any{
			e.send("response.reasoning_text.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": 0, "text": text}),
			e.send("response.content_part.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": 0, "part": map[string]any{"type": "reasoning_text", "text": text}}),
			e.send("response.output_item.done", map[string]any{"output_index": item.index, "item": e.renderItem(item)}),
		}
	case itemMessage:
		var out []map[string]any
		if item.textAdded {
			text := item.text.String()
			out = append(out,
				e.send("response.output_text.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": item.textContent, "text": text}),
				e.send("response.content_part.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": item.textContent, "part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}),
			)
		}
		if item.refusalAdded {
			refusal := item.refusal.String()
			out = append(out,
				e.send("response.refusal.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": item.refusalIndex, "refusal": refusal}),
				e.send("response.content_part.done", map[string]any{"item_id": item.id, "output_index": item.index, "content_index": item.refusalIndex, "part": map[string]any{"type": "refusal", "refusal": refusal}}),
			)
		}
		return append(out, e.send("response.output_item.done", map[string]any{"output_index": item.index, "item": e.renderItem(item)}))
	case itemTool:
		return []map[string]any{
			e.send("response.function_call_arguments.done", map[string]any{"item_id": item.id, "output_index": item.index, "arguments": item.args.String()}),
			e.send("response.output_item.done", map[string]any{"output_index": item.index, "item": e.renderItem(item)}),
		}
	}
	return nil
}

// closeItems closes every open item in output order, with status.
func (e *StreamEmitter) closeItems(status string) []map[string]any {
	var out []map[string]any
	e.reasoning = nil
	for _, item := range e.items {
		if item.status != "" || item.kind == itemRaw {
			continue
		}
		out = append(out, e.closeItem(item, status)...)
	}
	return out
}

func (e *StreamEmitter) renderItem(item *outputItem) any {
	status := item.status
	if status == "" {
		status = "completed"
	}
	switch item.kind {
	case itemReasoning:
		return dto.NewResponsesReasoningItem(item.id, status, []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: item.reasoning.String()}})
	case itemTool:
		return map[string]any{"type": "function_call", "id": item.id, "call_id": item.callID, "name": item.name, "arguments": item.args.String(), "status": status}
	case itemRaw:
		return item.raw
	}
	content := make([]dto.ResponsesOutputContent, item.nextContent)
	if item.textAdded {
		content[item.textContent] = dto.ResponsesOutputContent{Type: "output_text", Text: item.text.String(), Annotations: []interface{}{}}
	}
	if item.refusalAdded {
		content[item.refusalIndex] = dto.ResponsesOutputContent{Type: "refusal", Refusal: item.refusal.String()}
	}
	return map[string]any{"type": "message", "id": item.id, "role": "assistant", "content": content, "status": status}
}

// envelope is the response object of a created, in_progress or terminal
// event: the output so far and the client's configuration echoed back.
func (e *StreamEmitter) envelope(status string) map[string]any {
	output := make([]any, 0, len(e.items))
	for _, item := range e.items {
		if item.status == "" && status != "in_progress" {
			item.status = status
		}
		if status == "in_progress" && item.status == "" {
			continue
		}
		output = append(output, e.renderItem(item))
	}
	if status == "in_progress" {
		output = []any{}
	}
	response := map[string]any{
		"id": e.responseID, "object": "response", "created_at": e.createdAt, "status": status, "model": e.model, "output": output, "usage": dto.ProjectResponsesUsage(e.usage),
		"max_output_tokens": nil, "parallel_tool_calls": true, "store": false, "temperature": float64(1), "tool_choice": defaultResponseToolChoice(e.request),
		"tools": []any{}, "top_p": float64(1), "truncation": "disabled",
	}
	applyResponseConfigurationEnvelope(response, e.request)
	return response
}

func applyResponseConfigurationEnvelope(response map[string]any, request *textgen.Request) {
	if response == nil || request == nil {
		return
	}
	wire := &dto.OpenAIResponsesRequest{}
	if err := encodeResponseConfiguration(request, wire); err != nil {
		return
	}
	copyRaw := func(key string, raw []byte) {
		if len(raw) == 0 {
			return
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			response[key] = value
		}
	}
	copyRaw("instructions", wire.Instructions)
	if request.Sampling.MaxOutput.Set {
		response["max_output_tokens"] = wire.MaxOutputTokens
	}
	copyRaw("parallel_tool_calls", wire.ParallelToolCalls)
	if wire.PreviousResponseID != "" {
		response["previous_response_id"] = wire.PreviousResponseID
	}
	if wire.Reasoning != nil {
		response["reasoning"] = wire.Reasoning
	}
	copyRaw("store", wire.Store)
	if wire.Temperature != nil {
		response["temperature"] = *wire.Temperature
	}
	copyRaw("tool_choice", wire.ToolChoice)
	copyRaw("tools", wire.Tools)
	if wire.TopP != nil {
		response["top_p"] = *wire.TopP
	}
	copyRaw("metadata", wire.Metadata)
	if request.Metadata.User != "" {
		response["user"] = request.Metadata.User
	}
}

func normalizeResponseID(value string) string {
	if strings.HasPrefix(value, "resp_") {
		return value
	}
	return "resp_" + stableSuffix(value)
}

func NormalizeChatCompletionID(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "chatcmpl-") {
		return value
	}
	value = strings.TrimPrefix(value, "resp_")
	if value == "" {
		value = "lazu"
	}
	return "chatcmpl-" + value
}
