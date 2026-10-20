package textgen

import (
	"encoding/json"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
)

// PartKind is what a Part of a collected reply holds.
type PartKind string

const (
	PartText      PartKind = "text"
	PartRefusal   PartKind = "refusal"
	PartReasoning PartKind = "reasoning"
	PartTool      PartKind = "tool"
	PartSealed    PartKind = "sealed"
)

// Part is one block of a collected reply, in the order it was said.
type Part struct {
	Kind PartKind
	// Text is the part's text: the answer, the refusal, the reasoning, or
	// a tool call's arguments.
	Text string
	// Signature is a reasoning part's, SealedBy the dialect it belongs to
	// (also a sealed part's).
	Signature string
	SealedBy  string
	Sealed    json.RawMessage
	// ItemID is the upstream's id for a reasoning block or call item.
	ItemID string
	// ToolIndex, CallID and Name are a tool part's.
	ToolIndex int
	CallID    string
	Name      string
}

// Result is a whole reply, collected from its events: what a non-streaming
// render reads, and what a streaming emitter keeps for its terminal event.
type Result struct {
	ResponseID string
	Model      string
	CreatedAt  int64
	Parts      []Part
	Stop       StopReason
	StopSeen   bool
	Usage      *dto.Usage
	Error      *SemanticError
}

// Collector assembles a Result from events. Every emitter can use one to
// know what the reply contained so far.
type Collector struct {
	result Result
	// fresh: a reasoning boundary said the next reasoning is a block of
	// its own
	fresh bool
	tools map[int]int // ToolIndex -> index in Parts
	// text, reasoning and args are the builders of the open parts, so a
	// long reply is not copied on every delta
	open map[int]*strings.Builder
}

func (c *Collector) part(kind PartKind) *Part {
	if n := len(c.result.Parts); n > 0 && c.result.Parts[n-1].Kind == kind {
		return &c.result.Parts[n-1]
	}
	return nil
}

func (c *Collector) grow(index int, text string) {
	if c.open == nil {
		c.open = map[int]*strings.Builder{}
	}
	builder := c.open[index]
	if builder == nil {
		builder = &strings.Builder{}
		builder.WriteString(c.result.Parts[index].Text)
		c.open[index] = builder
	}
	builder.WriteString(text)
}

func (c *Collector) flush() {
	for index, builder := range c.open {
		c.result.Parts[index].Text = builder.String()
	}
	c.open = nil
}

func (c *Collector) appendPart(part Part) int {
	c.flush()
	c.result.Parts = append(c.result.Parts, part)
	return len(c.result.Parts) - 1
}

// Add folds events into the reply.
func (c *Collector) Add(events ...Event) {
	for _, event := range events {
		switch event.Kind {
		case EventStart:
			if event.ResponseID != "" {
				c.result.ResponseID = event.ResponseID
			}
			if event.Model != "" {
				c.result.Model = event.Model
			}
			if event.CreatedAt > 0 {
				c.result.CreatedAt = event.CreatedAt
			}
		case EventText, EventRefusal:
			kind := PartText
			if event.Kind == EventRefusal {
				kind = PartRefusal
			}
			if event.Text == "" {
				continue
			}
			if c.part(kind) != nil {
				c.grow(len(c.result.Parts)-1, event.Text)
			} else {
				c.appendPart(Part{Kind: kind, Text: event.Text})
			}
		case EventReasoningStart:
			c.fresh = true
			continue
		case EventReasoning:
			if event.Text == "" {
				continue
			}
			if p := c.part(PartReasoning); p != nil && !c.fresh {
				c.grow(len(c.result.Parts)-1, event.Text)
			} else {
				c.appendPart(Part{Kind: PartReasoning, Text: event.Text, ItemID: event.ItemID})
			}
		case EventReasoningSignature:
			if p := c.part(PartReasoning); p != nil && !c.fresh {
				p.Signature += event.Text
				p.SealedBy = event.SealedBy
			} else {
				c.appendPart(Part{Kind: PartReasoning, Signature: event.Text, SealedBy: event.SealedBy})
			}
		case EventReasoningSealed:
			c.appendPart(Part{Kind: PartSealed, Sealed: append(json.RawMessage(nil), event.Sealed...), SealedBy: event.SealedBy, ItemID: event.ItemID})
		case EventToolStart:
			if c.tools == nil {
				c.tools = map[int]int{}
			}
			if _, open := c.tools[event.ToolIndex]; open {
				continue
			}
			c.tools[event.ToolIndex] = c.appendPart(Part{Kind: PartTool, ToolIndex: event.ToolIndex, CallID: event.ToolCallID, Name: event.ToolName, ItemID: event.ItemID})
		case EventToolArguments, EventToolDone:
			index, open := c.tools[event.ToolIndex]
			if !open {
				continue
			}
			p := &c.result.Parts[index]
			if event.ToolCallID != "" {
				p.CallID = event.ToolCallID
			}
			if event.ToolName != "" {
				p.Name = event.ToolName
			}
			if event.Kind == EventToolDone {
				c.flush()
				if event.Text != "" && c.result.Parts[index].Text == "" {
					c.result.Parts[index].Text = event.Text
				}
				continue
			}
			if event.Text != "" {
				c.grow(index, event.Text)
			}
		case EventStop:
			c.result.Stop, c.result.StopSeen = event.Stop, true
		case EventUsage:
			c.result.Usage = MergeUsage(c.result.Usage, event.Usage)
		case EventError:
			c.result.Error = event.Error
		}
		if event.Kind != EventStart && event.Kind != EventUsage {
			c.fresh = false
		}
	}
}

// Result is the reply so far. A reply of tool calls that said it ended
// normally (a relay's end_turn beside its tool_use blocks) is the client's
// to run the calls of, so its stop is tool.
func (c *Collector) Result() Result {
	c.flush()
	result := c.result
	result.Parts = append([]Part(nil), c.result.Parts...)
	if !result.StopSeen || result.Stop == StopEnd {
		for _, part := range result.Parts {
			if part.Kind == PartTool {
				result.Stop = StopTool
			}
		}
	}
	return result
}

// MergeUsage is base with every non-zero count of update taken over it:
// a stream reports input on its first event and output on its last.
func MergeUsage(base, update *dto.Usage) *dto.Usage {
	if update == nil {
		return base
	}
	if base == nil {
		copied := *update
		// the detail pointers are the collector's own, merged into later
		if update.InputTokensDetails != nil {
			details := *update.InputTokensDetails
			copied.InputTokensDetails = &details
		}
		if update.OutputTokensDetails != nil {
			details := *update.OutputTokensDetails
			copied.OutputTokensDetails = &details
		}
		if copied.TotalTokens < copied.PromptTokens+copied.CompletionTokens {
			copied.TotalTokens = copied.PromptTokens + copied.CompletionTokens
		}
		return &copied
	}
	if update.PromptTokens > 0 {
		base.PromptTokens = update.PromptTokens
		base.PromptExcludesCache = update.PromptExcludesCache
		base.InputTokenSource = update.InputTokenSource
	}
	if update.CompletionTokens > 0 {
		base.CompletionTokens = update.CompletionTokens
		base.OutputTokenSource = update.OutputTokenSource
	}
	if update.TotalTokens > 0 {
		base.TotalTokens = update.TotalTokens
	}
	mergeInputDetails(&base.PromptTokensDetails, update.PromptTokensDetails)
	mergeOutputDetails(&base.CompletionTokenDetails, update.CompletionTokenDetails)
	if update.InputTokensDetails != nil {
		if base.InputTokensDetails == nil {
			base.InputTokensDetails = &dto.InputTokenDetails{}
		}
		mergeInputDetails(base.InputTokensDetails, *update.InputTokensDetails)
	}
	if update.OutputTokensDetails != nil {
		if base.OutputTokensDetails == nil {
			base.OutputTokensDetails = &dto.OutputTokenDetails{}
		}
		mergeOutputDetails(base.OutputTokensDetails, *update.OutputTokensDetails)
	}
	mergeCount(&base.InputTokens, update.InputTokens)
	mergeCount(&base.OutputTokens, update.OutputTokens)
	mergeCount(&base.PromptCacheHitTokens, update.PromptCacheHitTokens)
	mergeCount(&base.PromptCacheMissTokens, update.PromptCacheMissTokens)
	if update.GeneratedImages != nil {
		base.GeneratedImages = update.GeneratedImages
	}
	if update.InputImages != nil {
		base.InputImages = update.InputImages
	}
	if update.ImageUsage != nil {
		base.ImageUsage = update.ImageUsage
	}
	if update.Cost != nil {
		base.Cost = update.Cost
	}
	if base.TotalTokens < base.PromptTokens+base.CompletionTokens {
		base.TotalTokens = base.PromptTokens + base.CompletionTokens
	}
	return base
}

// mergeCount takes a later count the provider reported; a zero is a
// dimension it did not report again, not a reset.
func mergeCount(base *int, update int) {
	if update > 0 {
		*base = update
	}
}

func mergeInputDetails(base *dto.InputTokenDetails, update dto.InputTokenDetails) {
	mergeCount(&base.CachedTokens, update.CachedTokens)
	mergeCount(&base.CachedTokensDetails.TextTokens, update.CachedTokensDetails.TextTokens)
	mergeCount(&base.CachedTokensDetails.AudioTokens, update.CachedTokensDetails.AudioTokens)
	mergeCount(&base.CachedTokensDetails.ImageTokens, update.CachedTokensDetails.ImageTokens)
	mergeCount(&base.CacheWriteTokens, update.CacheWriteTokens)
	mergeCount(&base.CacheMissTokens, update.CacheMissTokens)
	mergeCount(&base.CachedCreationTokens, update.CachedCreationTokens)
	mergeCount(&base.CachedCreation5mTokens, update.CachedCreation5mTokens)
	mergeCount(&base.CachedCreation1hTokens, update.CachedCreation1hTokens)
	mergeCount(&base.TextTokens, update.TextTokens)
	mergeCount(&base.AudioTokens, update.AudioTokens)
	mergeCount(&base.ImageTokens, update.ImageTokens)
}

func mergeOutputDetails(base *dto.OutputTokenDetails, update dto.OutputTokenDetails) {
	mergeCount(&base.TextTokens, update.TextTokens)
	mergeCount(&base.AudioTokens, update.AudioTokens)
	mergeCount(&base.ImageTokens, update.ImageTokens)
	mergeCount(&base.ReasoningTokens, update.ReasoningTokens)
	mergeCount(&base.AcceptedPredictionTokens, update.AcceptedPredictionTokens)
	mergeCount(&base.RejectedPredictionTokens, update.RejectedPredictionTokens)
}
