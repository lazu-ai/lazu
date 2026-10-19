package textgen

import "github.com/lazu-ai/lazu/internal/dto"

type EventKind string

const (
	EventResponseCreated    EventKind = "response_created"
	EventResponseInProgress EventKind = "response_in_progress"
	EventItemAdded          EventKind = "item_added"
	EventContentPartAdded   EventKind = "content_part_added"
	EventDelta              EventKind = "delta"
	EventDeltaDone          EventKind = "delta_done"
	EventContentPartDone    EventKind = "content_part_done"
	EventItemDone           EventKind = "item_done"
	EventResponseCompleted  EventKind = "response_completed"
	EventResponseIncomplete EventKind = "response_incomplete"
	EventResponseFailed     EventKind = "response_failed"
	EventUsage              EventKind = "usage"
)

type DeltaKind string

const (
	DeltaText          DeltaKind = "text"
	DeltaRefusal       DeltaKind = "refusal"
	DeltaReasoning     DeltaKind = "reasoning"
	DeltaToolArguments DeltaKind = "tool_arguments"
	DeltaAnnotation    DeltaKind = "annotation"
)

type TypedDelta struct {
	Kind     DeltaKind
	Text     string
	Refusal  string
	ToolName string
}
type ResponseStatus string

const (
	StatusInProgress ResponseStatus = "in_progress"
	StatusCompleted  ResponseStatus = "completed"
	StatusIncomplete ResponseStatus = "incomplete"
	StatusFailed     ResponseStatus = "failed"
)

type SemanticError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ResponseOutputItem struct {
	Kind        InputKind
	OutputIndex int
	ID          string
	Status      ResponseStatus
	Role        string
	Text        string
	Refusal     string
	CallID      string
	Name        string
	Arguments   string
	Content     []dto.ResponsesOutputContent
}

type Event struct {
	Kind             EventKind
	Sequence         uint64
	ResponseID       string
	Model            string
	CreatedAt        int64
	ItemID           string
	ItemIndex        int
	ContentIndex     int
	ToolCallID       string
	Delta            TypedDelta
	Usage            *dto.Usage
	Status           ResponseStatus
	IncompleteReason string
	Metadata         map[string]string
	Error            *SemanticError
	Output           []ResponseOutputItem
	Content          []dto.ResponsesOutputContent
}
