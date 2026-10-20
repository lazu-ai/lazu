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
	// URL downloads the file straight from storage. For a stored file it is a
	// signed link that works until URLExpiresAt; read the job again for a
	// fresh one.
	URL          string `json:"url,omitempty"`
	URLExpiresAt int64  `json:"url_expires_at,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	// ExpiresAt is when the stored file is deleted.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

type MediaJobError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type MediaJobListResponse struct {
	Object string           `json:"object"`
	Data   []MediaJobObject `json:"data"`
}

// ConsoleTaskObject is one row of the console's Tasks view: the job as the API
// shows it, plus what the console needs to read it like a usage-log row.
type ConsoleTaskObject struct {
	MediaJobObject
	TokenName string          `json:"token_name,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	Spec      ConsoleTaskSpec `json:"spec"`
	// HoldMicrousd is what submission held; ChargeMicrousd what settlement
	// charged; RefundMicrousd what went back (the whole hold on failure).
	HoldMicrousd   int64 `json:"hold_microusd"`
	ChargeMicrousd int64 `json:"charge_microusd"`
	RefundMicrousd int64 `json:"refund_microusd"`
	// FileExpired marks a finished job whose stored file has been deleted.
	FileExpired bool `json:"file_expired,omitempty"`
}

// ConsoleTaskSpec is the request's shape in a few fields.
type ConsoleTaskSpec struct {
	Seconds         int    `json:"seconds,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
	AspectRatio     string `json:"aspect_ratio,omitempty"`
	Audio           *bool  `json:"audio,omitempty"`
	ReferenceImages int    `json:"reference_images,omitempty"`
	ReferenceVideos int    `json:"reference_videos,omitempty"`
	ReferenceAudios int    `json:"reference_audios,omitempty"`
	Frames          int    `json:"frames,omitempty"`
}
