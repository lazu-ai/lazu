package dto

// ResponsesReasoningItem is shared by JSON responses, SSE item events and
// terminal envelopes. Summary is required even when no summary was generated.
type ResponsesReasoningItem struct {
	Type    string                      `json:"type"`
	ID      string                      `json:"id"`
	Status  string                      `json:"status"`
	Summary []ResponsesReasoningSummary `json:"summary"`
	Content []ResponsesOutputContent    `json:"content"`
}

type ResponsesReasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewResponsesReasoningItem(id, status string, content []ResponsesOutputContent) ResponsesReasoningItem {
	if content == nil {
		content = []ResponsesOutputContent{}
	}
	return ResponsesReasoningItem{Type: "reasoning", ID: id, Status: status, Summary: []ResponsesReasoningSummary{}, Content: content}
}
