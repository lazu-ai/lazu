package dto

import "encoding/json"

type MediaJobObject struct {
	ID           string            `json:"id"`
	Object       string            `json:"object"`
	Kind         string            `json:"kind"`
	Source       string            `json:"source,omitempty"`
	Model        string            `json:"model"`
	Provider     string            `json:"provider,omitempty"`
	Status       string            `json:"status"`
	CreatedAt    int64             `json:"created_at"`
	UpdatedAt    int64             `json:"updated_at,omitempty"`
	StartedAt    int64             `json:"started_at,omitempty"`
	CompletedAt  int64             `json:"completed_at,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	AgentID      string            `json:"agent_id,omitempty"`
	RequestID    string            `json:"request_id,omitempty"`
	Artifact     *MediaJobArtifact `json:"artifact,omitempty"`
	Error        *MediaJobError    `json:"error,omitempty"`
	Request      json.RawMessage   `json:"request,omitempty"`
	CostSnapshot json.RawMessage   `json:"cost_snapshot,omitempty"`
	// Usage is what the provider reported about a finished video: seconds,
	// resolution, frame rate and token count. Procurement cost is never here.
	Usage *MediaJobUsage `json:"usage,omitempty"`
}

type MediaJobArtifact struct {
	FileID string `json:"file_id,omitempty"`
	URL    string `json:"url,omitempty"`
}

type MediaJobError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type MediaJobListResponse struct {
	Object string           `json:"object"`
	Data   []MediaJobObject `json:"data"`
}
