package openai_responses

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

type toolStreamState struct {
	ItemID      string
	CallID      string
	Name        string
	Arguments   strings.Builder
	OutputIndex int
	Added       bool
}

const (
	defaultChatStreamRetainedOutputBytes = 16 << 20
	minimumChatStreamRetainedOutputBytes = 64 << 10
	chatStreamBytesPerOutputToken        = 16
)

// ChatStreamBridge is an explicit state machine for Chat-upstream to
// Responses-client SSE conversion. Responses terminal events require the
// completed output, so retained text is bounded per request.
type ChatStreamBridge struct {
	sequence              uint64
	responseID            string
	model                 string
	createdAt             int64
	started               bool
	assistantRoleSeen     bool
	messageAdded          bool
	textContentAdded      bool
	refusalContentAdded   bool
	messageText           strings.Builder
	messageRefusal        strings.Builder
	reasoningText         strings.Builder
	messageItemID         string
	reasoningItemID       string
	messageOutputIndex    int
	reasoningOutputIndex  int
	textContentIndex      int
	refusalContentIndex   int
	reasoningContentIndex int
	nextContentIndex      int
	nextOutputIndex       int
	finishReason          string
	reasoningAdded        bool
	reasoningContentAdded bool
	terminal              bool
	tools                 map[int]*toolStreamState
	usage                 *dto.Usage
	retainedOutputBytes   int
	retainedOutputLimit   int
}

func NewChatStreamBridge(responseID, model string, retainedOutputLimit ...int) *ChatStreamBridge {
	if responseID == "" {
		responseID = "resp_lazu"
	}
	limit := defaultChatStreamRetainedOutputBytes
	if len(retainedOutputLimit) > 0 && retainedOutputLimit[0] > 0 {
		limit = retainedOutputLimit[0]
	}
	return &ChatStreamBridge{responseID: normalizeResponseID(responseID), model: model, createdAt: time.Now().Unix(), messageItemID: "msg_" + stableSuffix(responseID), reasoningItemID: "rs_lazu_" + stableSuffix(responseID), tools: map[int]*toolStreamState{}, retainedOutputLimit: limit}
}

func ChatStreamRetainedOutputLimit(request *textgen.Request) int {
	if request == nil || !request.Sampling.MaxOutput.Set || request.Sampling.MaxOutput.Value == 0 {
		return defaultChatStreamRetainedOutputBytes
	}
	requested := uint64(request.Sampling.MaxOutput.Value)
	maxDynamicTokens := uint64((defaultChatStreamRetainedOutputBytes - minimumChatStreamRetainedOutputBytes) / chatStreamBytesPerOutputToken)
	if requested > maxDynamicTokens {
		return defaultChatStreamRetainedOutputBytes
	}
	limit := requested*chatStreamBytesPerOutputToken + minimumChatStreamRetainedOutputBytes
	return int(limit)
}

func (s *ChatStreamBridge) Consume(chunk *dto.ChatCompletionsStreamResponse) ([]textgen.Event, error) {
	if s == nil || chunk == nil {
		return nil, nil
	}
	if chunk.Id != "" && !s.started {
		s.responseID = normalizeResponseID(chunk.Id)
		s.messageItemID = "msg_" + stableSuffix(chunk.Id)
		s.reasoningItemID = "rs_lazu_" + stableSuffix(chunk.Id)
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.Created > 0 {
		s.createdAt = chunk.Created
	}
	events := s.startEvents()
	if chunk.Usage != nil {
		s.usage = chunk.Usage
		events = append(events, s.event(textgen.EventUsage))
	}
	for _, choice := range chunk.Choices {
		if strings.EqualFold(strings.TrimSpace(choice.Delta.Role), "assistant") {
			s.assistantRoleSeen = true
		}
		if reasoning := choice.Delta.GetReasoningContent(); reasoning != "" {
			if err := s.retainOutput(reasoning); err != nil {
				return events, err
			}
			partEvents, contentIndex := s.ensureReasoning()
			events = append(events, partEvents...)
			s.reasoningText.WriteString(reasoning)
			events = append(events, textgen.Event{Kind: textgen.EventDelta, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.reasoningItemID, ItemIndex: s.reasoningOutputIndex, ContentIndex: contentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaReasoning, Text: reasoning}})
		}
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			if err := s.retainOutput(*choice.Delta.Content); err != nil {
				return events, err
			}
			partEvents, contentIndex := s.ensureMessage(textgen.DeltaText)
			events = append(events, partEvents...)
			s.messageText.WriteString(*choice.Delta.Content)
			events = append(events, textgen.Event{Kind: textgen.EventDelta, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, ContentIndex: contentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaText, Text: *choice.Delta.Content}})
		}
		if choice.Delta.Refusal != nil && *choice.Delta.Refusal != "" {
			if err := s.retainOutput(*choice.Delta.Refusal); err != nil {
				return events, err
			}
			partEvents, contentIndex := s.ensureMessage(textgen.DeltaRefusal)
			events = append(events, partEvents...)
			s.messageRefusal.WriteString(*choice.Delta.Refusal)
			events = append(events, textgen.Event{Kind: textgen.EventDelta, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, ContentIndex: contentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaRefusal, Refusal: *choice.Delta.Refusal}})
		}
		for _, call := range choice.Delta.ToolCalls {
			if call.Function.Arguments != "" {
				if err := s.retainOutput(call.Function.Arguments); err != nil {
					return events, err
				}
			}
			index := 0
			if call.Index != nil {
				index = *call.Index
			}
			state := s.tools[index]
			if state == nil {
				state = &toolStreamState{ItemID: fmt.Sprintf("fc_%s_%d", stableSuffix(s.responseID), index), CallID: call.ID, Name: call.Function.Name, OutputIndex: s.nextOutputIndex}
				s.nextOutputIndex++
				s.tools[index] = state
			}
			if call.ID != "" {
				state.CallID = call.ID
			}
			if call.Function.Name != "" {
				state.Name = call.Function.Name
			}
			if !state.Added {
				state.Added = true
				events = append(events, textgen.Event{Kind: textgen.EventItemAdded, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: state.ItemID, ItemIndex: state.OutputIndex, ToolCallID: state.CallID, Delta: textgen.TypedDelta{Kind: textgen.DeltaToolArguments, ToolName: state.Name}})
			}
			if call.Function.Arguments != "" {
				state.Arguments.WriteString(call.Function.Arguments)
				events = append(events, textgen.Event{Kind: textgen.EventDelta, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: state.ItemID, ItemIndex: state.OutputIndex, ToolCallID: state.CallID, Delta: textgen.TypedDelta{Kind: textgen.DeltaToolArguments, Text: call.Function.Arguments, ToolName: state.Name}})
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			s.finishReason = *choice.FinishReason
		}
	}
	return events, nil
}

func (s *ChatStreamBridge) retainOutput(value string) error {
	if s == nil || value == "" {
		return nil
	}
	if s.retainedOutputLimit <= 0 {
		s.retainedOutputLimit = defaultChatStreamRetainedOutputBytes
	}
	if len(value) > s.retainedOutputLimit-s.retainedOutputBytes {
		return fmt.Errorf("bridged stream retained output exceeds %d-byte limit", s.retainedOutputLimit)
	}
	s.retainedOutputBytes += len(value)
	return nil
}

func (s *ChatStreamBridge) Finalize(usage *dto.Usage) ([]textgen.Event, error) {
	if s == nil || s.terminal {
		return nil, nil
	}
	if strings.TrimSpace(s.finishReason) == "" {
		return nil, fmt.Errorf("chat stream ended without finish_reason")
	}
	if usage != nil {
		s.usage = chatUsageToResponses(usage)
	}
	events := s.startEvents()
	terminalKind := textgen.EventResponseCompleted
	status := textgen.StatusCompleted
	reason := ""
	switch strings.ToLower(strings.TrimSpace(s.finishReason)) {
	case "length":
		terminalKind = textgen.EventResponseIncomplete
		status = textgen.StatusIncomplete
		reason = "max_output_tokens"
	case "content_filter":
		terminalKind = textgen.EventResponseIncomplete
		status = textgen.StatusIncomplete
		reason = "content_filter"
	}
	if s.assistantRoleSeen && !s.messageAdded && len(s.tools) == 0 {
		messageEvents, _ := s.ensureMessage(textgen.DeltaText)
		events = append(events, messageEvents...)
	}
	closedEvents, outputs := s.closeActiveItems(status)
	events = append(events, closedEvents...)
	events = append(events, textgen.Event{Kind: terminalKind, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, Status: status, IncompleteReason: reason, Usage: s.usage, Output: outputs})
	s.terminal = true
	return events, nil
}

func (s *ChatStreamBridge) closeActiveItems(status textgen.ResponseStatus) ([]textgen.Event, []textgen.ResponseOutputItem) {
	events := make([]textgen.Event, 0)
	if s.reasoningContentAdded {
		text := s.reasoningText.String()
		events = append(events,
			textgen.Event{Kind: textgen.EventDeltaDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.reasoningItemID, ItemIndex: s.reasoningOutputIndex, ContentIndex: s.reasoningContentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaReasoning, Text: text}},
			textgen.Event{Kind: textgen.EventContentPartDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.reasoningItemID, ItemIndex: s.reasoningOutputIndex, ContentIndex: s.reasoningContentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaReasoning, Text: text}},
		)
	}
	if s.reasoningAdded {
		events = append(events, textgen.Event{Kind: textgen.EventItemDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, Status: status, ItemID: s.reasoningItemID, ItemIndex: s.reasoningOutputIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaReasoning, Text: s.reasoningText.String()}})
	}
	if s.textContentAdded {
		text := s.messageText.String()
		events = append(events,
			textgen.Event{Kind: textgen.EventDeltaDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, ContentIndex: s.textContentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaText, Text: text}},
			textgen.Event{Kind: textgen.EventContentPartDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, ContentIndex: s.textContentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaText, Text: text}},
		)
	}
	if s.refusalContentAdded {
		refusal := s.messageRefusal.String()
		events = append(events,
			textgen.Event{Kind: textgen.EventDeltaDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, ContentIndex: s.refusalContentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaRefusal, Refusal: refusal}},
			textgen.Event{Kind: textgen.EventContentPartDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, ContentIndex: s.refusalContentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaRefusal, Refusal: refusal}},
		)
	}
	if s.messageAdded {
		events = append(events, textgen.Event{Kind: textgen.EventItemDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, Status: status, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaText, Text: s.messageText.String(), Refusal: s.messageRefusal.String()}, Content: s.messageContent()})
	}
	toolIndexes := make([]int, 0, len(s.tools))
	for index := range s.tools {
		toolIndexes = append(toolIndexes, index)
	}
	sort.Slice(toolIndexes, func(i, j int) bool { return s.tools[toolIndexes[i]].OutputIndex < s.tools[toolIndexes[j]].OutputIndex })
	for _, index := range toolIndexes {
		state := s.tools[index]
		if state == nil || !state.Added {
			continue
		}
		arguments := state.Arguments.String()
		events = append(events,
			textgen.Event{Kind: textgen.EventDeltaDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, ItemID: state.ItemID, ItemIndex: state.OutputIndex, ToolCallID: state.CallID, Delta: textgen.TypedDelta{Kind: textgen.DeltaToolArguments, Text: arguments, ToolName: state.Name}},
			textgen.Event{Kind: textgen.EventItemDone, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, Status: status, ItemID: state.ItemID, ItemIndex: state.OutputIndex, ToolCallID: state.CallID, Delta: textgen.TypedDelta{Kind: textgen.DeltaToolArguments, Text: arguments, ToolName: state.Name}},
		)
	}
	outputs := make([]textgen.ResponseOutputItem, 0, 2+len(s.tools))
	if s.reasoningAdded {
		outputs = append(outputs, textgen.ResponseOutputItem{Kind: textgen.InputReasoning, OutputIndex: s.reasoningOutputIndex, ID: s.reasoningItemID, Status: status, Text: s.reasoningText.String()})
	}
	if s.messageAdded {
		outputs = append(outputs, textgen.ResponseOutputItem{Kind: textgen.InputMessage, OutputIndex: s.messageOutputIndex, ID: s.messageItemID, Status: status, Role: "assistant", Text: s.messageText.String(), Refusal: s.messageRefusal.String(), Content: s.messageContent()})
	}
	for _, index := range toolIndexes {
		state := s.tools[index]
		if state != nil && state.Added {
			outputs = append(outputs, textgen.ResponseOutputItem{Kind: textgen.InputFunctionCall, OutputIndex: state.OutputIndex, ID: state.ItemID, Status: status, CallID: state.CallID, Name: state.Name, Arguments: state.Arguments.String()})
		}
	}
	sort.SliceStable(outputs, func(i, j int) bool { return outputs[i].OutputIndex < outputs[j].OutputIndex })
	return events, outputs
}

func (s *ChatStreamBridge) Fail(err error, usage ...*dto.Usage) []textgen.Event {
	message := "stream failed"
	if err != nil {
		message = err.Error()
	}
	return s.FailWithSemanticError(&textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: message}, usage...)
}

func (s *ChatStreamBridge) FailWithSemanticError(streamError *textgen.SemanticError, usage ...*dto.Usage) []textgen.Event {
	if s == nil || s.terminal {
		return nil
	}
	if len(usage) > 0 && usage[0] != nil {
		s.usage = chatUsageToResponses(usage[0])
	}
	events := s.startEvents()
	s.terminal = true
	if streamError == nil {
		streamError = &textgen.SemanticError{Type: "api_error", Code: "upstream_stream_failed", Message: "The model stopped the stream with an error. Check any partial output before retrying."}
	}
	closedEvents, outputs := s.closeActiveItems(textgen.StatusIncomplete)
	events = append(events, closedEvents...)
	return append(events, textgen.Event{Kind: textgen.EventResponseFailed, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, Status: textgen.StatusFailed, Usage: s.usage, Output: outputs, Error: streamError})
}

// VisitBillableOutput exposes already-retained logical output without creating
// a second stream-sized buffer. Each callback value is one semantic field, so
// token estimates are independent of arbitrary SSE chunk boundaries.
func (s *ChatStreamBridge) VisitBillableOutput(visit func(string)) int {
	if s == nil {
		return 0
	}
	if visit != nil {
		if s.reasoningText.Len() > 0 {
			visit(s.reasoningText.String())
		}
		if s.messageText.Len() > 0 {
			visit(s.messageText.String())
		}
		if s.messageRefusal.Len() > 0 {
			visit(s.messageRefusal.String())
		}
	}
	toolCount := 0
	for _, state := range s.tools {
		if state == nil || !state.Added {
			continue
		}
		toolCount++
		if visit != nil {
			if state.Name != "" {
				visit(state.Name)
			}
			if state.Arguments.Len() > 0 {
				visit(state.Arguments.String())
			}
		}
	}
	return toolCount
}

func (s *ChatStreamBridge) startEvents() []textgen.Event {
	if s.started {
		return nil
	}
	s.started = true
	return []textgen.Event{{Kind: textgen.EventResponseCreated, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, Status: textgen.StatusInProgress}, {Kind: textgen.EventResponseInProgress, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, CreatedAt: s.createdAt, Status: textgen.StatusInProgress}}
}
func (s *ChatStreamBridge) ensureMessage(kind textgen.DeltaKind) ([]textgen.Event, int) {
	events := make([]textgen.Event, 0, 2)
	if !s.messageAdded {
		s.messageAdded = true
		s.messageOutputIndex = s.nextOutputIndex
		s.nextOutputIndex++
		events = append(events, textgen.Event{Kind: textgen.EventItemAdded, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex})
	}
	contentIndex := s.textContentIndex
	added := s.textContentAdded
	if kind == textgen.DeltaRefusal {
		contentIndex = s.refusalContentIndex
		added = s.refusalContentAdded
	}
	if !added {
		contentIndex = s.nextContentIndex
		s.nextContentIndex++
		if kind == textgen.DeltaRefusal {
			s.refusalContentAdded = true
			s.refusalContentIndex = contentIndex
		} else {
			s.textContentAdded = true
			s.textContentIndex = contentIndex
		}
		events = append(events, textgen.Event{Kind: textgen.EventContentPartAdded, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.messageItemID, ItemIndex: s.messageOutputIndex, ContentIndex: contentIndex, Delta: textgen.TypedDelta{Kind: kind}})
	}
	return events, contentIndex
}

// Content indexes belong to the message, including refusal-first streams.
func (s *ChatStreamBridge) messageContent() []dto.ResponsesOutputContent {
	content := make([]dto.ResponsesOutputContent, s.nextContentIndex)
	if s.textContentAdded {
		content[s.textContentIndex] = dto.ResponsesOutputContent{Type: "output_text", Text: s.messageText.String(), Annotations: []interface{}{}}
	}
	if s.refusalContentAdded {
		content[s.refusalContentIndex] = dto.ResponsesOutputContent{Type: "refusal", Refusal: s.messageRefusal.String()}
	}
	return content
}

func (s *ChatStreamBridge) ensureReasoning() ([]textgen.Event, int) {
	events := make([]textgen.Event, 0, 2)
	if !s.reasoningAdded {
		s.reasoningAdded = true
		s.reasoningOutputIndex = s.nextOutputIndex
		s.nextOutputIndex++
		events = append(events, textgen.Event{Kind: textgen.EventItemAdded, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.reasoningItemID, ItemIndex: s.reasoningOutputIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaReasoning}})
	}
	if !s.reasoningContentAdded {
		s.reasoningContentAdded = true
		s.reasoningContentIndex = 0
		events = append(events, textgen.Event{Kind: textgen.EventContentPartAdded, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, ItemID: s.reasoningItemID, ItemIndex: s.reasoningOutputIndex, ContentIndex: s.reasoningContentIndex, Delta: textgen.TypedDelta{Kind: textgen.DeltaReasoning}})
	}
	return events, s.reasoningContentIndex
}
func (s *ChatStreamBridge) event(kind textgen.EventKind) textgen.Event {
	return textgen.Event{Kind: kind, Sequence: s.next(), ResponseID: s.responseID, Model: s.model, Usage: s.usage}
}
func (s *ChatStreamBridge) next() uint64 { value := s.sequence; s.sequence++; return value }

func EmitEvent(event textgen.Event, semanticRequest ...*textgen.Request) map[string]any {
	var request *textgen.Request
	if len(semanticRequest) > 0 {
		request = semanticRequest[0]
	}
	base := map[string]any{"sequence_number": event.Sequence}
	switch event.Kind {
	case textgen.EventResponseCreated:
		base["type"] = "response.created"
		base["response"] = responseEnvelope(event, "in_progress", request)
	case textgen.EventResponseInProgress:
		base["type"] = "response.in_progress"
		base["response"] = responseEnvelope(event, "in_progress", request)
	case textgen.EventItemAdded:
		base["type"] = "response.output_item.added"
		base["output_index"] = event.ItemIndex
		if event.Delta.Kind == textgen.DeltaToolArguments {
			base["item"] = map[string]any{"type": "function_call", "id": event.ItemID, "call_id": event.ToolCallID, "name": event.Delta.ToolName, "arguments": "", "status": "in_progress"}
		} else if event.Delta.Kind == textgen.DeltaReasoning {
			base["item"] = dto.NewResponsesReasoningItem(event.ItemID, "in_progress", nil)
		} else {
			base["item"] = map[string]any{"type": "message", "id": event.ItemID, "role": "assistant", "content": []any{}, "status": "in_progress"}
		}
	case textgen.EventContentPartAdded:
		base["type"] = "response.content_part.added"
		base["item_id"] = event.ItemID
		base["output_index"] = event.ItemIndex
		base["content_index"] = event.ContentIndex
		if event.Delta.Kind == textgen.DeltaRefusal {
			base["part"] = map[string]any{"type": "refusal", "refusal": ""}
		} else if event.Delta.Kind == textgen.DeltaReasoning {
			base["part"] = map[string]any{"type": "reasoning_text", "text": ""}
		} else {
			base["part"] = map[string]any{"type": "output_text", "text": "", "annotations": []any{}}
		}
	case textgen.EventDelta:
		base["item_id"] = event.ItemID
		base["output_index"] = event.ItemIndex
		if event.Delta.Kind == textgen.DeltaToolArguments {
			base["type"] = "response.function_call_arguments.delta"
			base["delta"] = event.Delta.Text
		} else if event.Delta.Kind == textgen.DeltaRefusal {
			base["type"] = "response.refusal.delta"
			base["content_index"] = event.ContentIndex
			base["delta"] = event.Delta.Refusal
		} else if event.Delta.Kind == textgen.DeltaReasoning {
			base["type"] = "response.reasoning_text.delta"
			base["content_index"] = event.ContentIndex
			base["delta"] = event.Delta.Text
		} else {
			base["type"] = "response.output_text.delta"
			base["content_index"] = event.ContentIndex
			base["delta"] = event.Delta.Text
		}
	case textgen.EventDeltaDone:
		base["item_id"] = event.ItemID
		base["output_index"] = event.ItemIndex
		switch event.Delta.Kind {
		case textgen.DeltaToolArguments:
			base["type"] = "response.function_call_arguments.done"
			base["arguments"] = event.Delta.Text
		case textgen.DeltaRefusal:
			base["type"] = "response.refusal.done"
			base["content_index"] = event.ContentIndex
			base["refusal"] = event.Delta.Refusal
		case textgen.DeltaReasoning:
			base["type"] = "response.reasoning_text.done"
			base["content_index"] = event.ContentIndex
			base["text"] = event.Delta.Text
		default:
			base["type"] = "response.output_text.done"
			base["content_index"] = event.ContentIndex
			base["text"] = event.Delta.Text
		}
	case textgen.EventContentPartDone:
		base["type"] = "response.content_part.done"
		base["item_id"] = event.ItemID
		base["output_index"] = event.ItemIndex
		base["content_index"] = event.ContentIndex
		if event.Delta.Kind == textgen.DeltaRefusal {
			base["part"] = map[string]any{"type": "refusal", "refusal": event.Delta.Refusal}
		} else if event.Delta.Kind == textgen.DeltaReasoning {
			base["part"] = map[string]any{"type": "reasoning_text", "text": event.Delta.Text}
		} else {
			base["part"] = map[string]any{"type": "output_text", "text": event.Delta.Text, "annotations": []any{}}
		}
	case textgen.EventItemDone:
		base["type"] = "response.output_item.done"
		base["output_index"] = event.ItemIndex
		itemStatus := string(event.Status)
		if itemStatus == "" {
			itemStatus = "completed"
		}
		if event.Delta.Kind == textgen.DeltaToolArguments {
			base["item"] = map[string]any{"type": "function_call", "id": event.ItemID, "call_id": event.ToolCallID, "name": event.Delta.ToolName, "arguments": event.Delta.Text, "status": itemStatus}
		} else if event.Delta.Kind == textgen.DeltaReasoning {
			base["item"] = dto.NewResponsesReasoningItem(event.ItemID, itemStatus, []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: event.Delta.Text}})
		} else {
			content := responseMessageContent(event.Delta.Text, event.Delta.Refusal, event.Content)
			base["item"] = map[string]any{"type": "message", "id": event.ItemID, "role": "assistant", "content": content, "status": itemStatus}
		}
	case textgen.EventResponseCompleted:
		base["type"] = "response.completed"
		base["response"] = responseEnvelope(event, "completed", request)
	case textgen.EventResponseIncomplete:
		base["type"] = "response.incomplete"
		response := responseEnvelope(event, "incomplete", request)
		response["incomplete_details"] = map[string]any{"reason": event.IncompleteReason}
		base["response"] = response
	case textgen.EventResponseFailed:
		base["type"] = "response.failed"
		response := responseEnvelope(event, "failed", request)
		response["error"] = event.Error
		base["response"] = response
	case textgen.EventUsage:
		base["type"] = "lazu.response.usage"
		base["usage"] = dto.ProjectResponsesUsage(event.Usage)
	}
	return base
}
func responseEnvelope(event textgen.Event, status string, request *textgen.Request) map[string]any {
	createdAt := event.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	output := make([]any, 0, len(event.Output))
	for _, item := range event.Output {
		itemStatus := string(item.Status)
		if itemStatus == "" {
			itemStatus = status
		}
		if item.Kind == textgen.InputFunctionCall {
			output = append(output, map[string]any{"type": "function_call", "id": item.ID, "call_id": item.CallID, "name": item.Name, "arguments": item.Arguments, "status": itemStatus})
			continue
		}
		if item.Kind == textgen.InputReasoning {
			output = append(output, dto.NewResponsesReasoningItem(item.ID, itemStatus, []dto.ResponsesOutputContent{{Type: "reasoning_text", Text: item.Text}}))
			continue
		}
		output = append(output, map[string]any{"type": "message", "id": item.ID, "role": item.Role, "content": responseMessageContent(item.Text, item.Refusal, item.Content), "status": itemStatus})
	}
	response := map[string]any{
		"id": event.ResponseID, "object": "response", "created_at": createdAt, "status": status, "model": event.Model, "output": output, "usage": dto.ProjectResponsesUsage(event.Usage),
		"max_output_tokens": nil, "parallel_tool_calls": true, "store": false, "temperature": float64(1), "tool_choice": defaultResponseToolChoice(request),
		"tools": []any{}, "top_p": float64(1), "truncation": "disabled",
	}
	applyResponseConfigurationEnvelope(response, request)
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

func responseMessageContent(text, refusal string, ordered []dto.ResponsesOutputContent) []dto.ResponsesOutputContent {
	if ordered != nil {
		return ordered
	}
	content := make([]dto.ResponsesOutputContent, 0, 2)
	if text != "" || refusal == "" {
		content = append(content, dto.ResponsesOutputContent{Type: "output_text", Text: text, Annotations: []interface{}{}})
	}
	if refusal != "" {
		content = append(content, dto.ResponsesOutputContent{Type: "refusal", Refusal: refusal})
	}
	return content
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
