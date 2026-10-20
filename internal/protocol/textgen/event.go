package textgen

import (
	"encoding/json"

	"github.com/lazu-ai/lazu/internal/dto"
)

// The response event IR: what a streamed reply said, as content, in the
// order it was said. It carries no protocol's lifecycle (Responses' items and
// content parts, Anthropic's blocks, Chat's choices): each protocol's stream
// decoder turns its wire into these events, and each protocol's emitter
// owns its own sequence numbers, ids, indexes and envelopes. A non-streaming
// reply is the same events, delivered at once.
//
// Only Usage is shared with the wire DTOs (dto.Usage is lazu's usage fact,
// not a vendor shape); TestEventIRCarriesNoWireShapes keeps it so.

type EventKind string

const (
	// EventStart opens a reply: ResponseID, Model and CreatedAt as the
	// upstream named them, any of them empty when it named none.
	EventStart EventKind = "start"
	// EventTurn says the assistant's turn began with nothing said yet (a
	// Chat chunk carrying only the role). An emitter whose protocol needs
	// a message item for an empty reply opens one on it; nothing else.
	EventTurn EventKind = "turn"
	// EventText is a fragment of the answer (Text).
	EventText EventKind = "text"
	// EventRefusal is a fragment of a refusal (Text).
	EventRefusal EventKind = "refusal"
	// EventReasoningStart opens a block of reasoning of its own: what
	// follows is not the previous block's, even with nothing between
	// them (Anthropic's thinking blocks, a Responses reasoning item, whose
	// id is ItemID when the upstream gave one).
	EventReasoningStart EventKind = "reasoning_start"
	// EventReasoning is a fragment of visible reasoning (Text).
	EventReasoning EventKind = "reasoning"
	// EventReasoningSignature is the signature of the open reasoning block
	// (Text), which only the dialect that signed it (SealedBy) may be
	// given back.
	EventReasoningSignature EventKind = "reasoning_signature"
	// EventReasoningSealed is reasoning the upstream sealed whole, as its
	// wire wrote it (Sealed), readable only by the dialect SealedBy.
	EventReasoningSealed EventKind = "reasoning_sealed"
	// EventToolStart opens a function call: ToolIndex is the upstream's
	// position for it (Chat merges fragments by it), ToolCallID and
	// ToolName what is known so far, ItemID the upstream's item id if any.
	EventToolStart EventKind = "tool_start"
	// EventToolArguments is a fragment of the call's arguments (Text) for
	// ToolIndex; ToolCallID and ToolName are set when the fragment carried
	// them (a Chat first fragment may not have).
	EventToolArguments EventKind = "tool_arguments"
	// EventToolDone says the upstream closed the call at ToolIndex; Text is
	// the whole arguments when the upstream restated them, "" otherwise.
	EventToolDone EventKind = "tool_done"
	// EventStop is why the reply ended (Stop).
	EventStop EventKind = "stop"
	// EventUsage carries the upstream's token counts so far (Usage).
	EventUsage EventKind = "usage"
	// EventError is the upstream failing the reply (Error); it ends it.
	EventError EventKind = "error"
)

// StopReason is why a reply ended, in protocol-neutral words.
type StopReason string

const (
	StopEnd    StopReason = "end"    // the model finished
	StopLength StopReason = "length" // the output limit was reached
	StopTool   StopReason = "tool"   // the model wants its tool calls run
	StopFilter StopReason = "filter" // a content filter cut it
)

type SemanticError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Event is one thing a streamed reply said.
type Event struct {
	Kind EventKind
	// Text is the fragment: answer, refusal, reasoning, signature or
	// arguments, by Kind.
	Text string
	// Role is EventTurn's role.
	Role string
	// ResponseID, Model and CreatedAt are EventStart's.
	ResponseID string
	Model      string
	CreatedAt  int64
	// ItemID is the upstream's id for a reasoning block or a call item.
	ItemID string
	// ToolIndex, ToolCallID and ToolName identify a call; see EventToolStart.
	ToolIndex  int
	ToolCallID string
	ToolName   string
	Stop       StopReason
	Usage      *dto.Usage
	Error      *SemanticError
	// Summary is EventReasoning's: the text summarizes the reasoning
	// (Responses' reasoning summary) rather than being it. Protocols with
	// one kind of reasoning text read both alike.
	Summary bool
	// Sealed and SealedBy are EventReasoningSealed's, SealedBy also
	// EventReasoningSignature's: the dialect the content belongs to.
	Sealed   json.RawMessage
	SealedBy string
}
