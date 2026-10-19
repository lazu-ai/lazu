package dto

import (
	"encoding/json"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

type VideoRequest struct {
	Model          string          `json:"model"`
	Prompt         string          `json:"prompt" binding:"required"`
	Seconds        int             `json:"seconds,omitempty"`
	Size           string          `json:"size,omitempty"`
	AspectRatio    string          `json:"aspect_ratio,omitempty"`
	Quality        string          `json:"quality,omitempty"`
	ResponseFormat string          `json:"response_format,omitempty"`
	User           json.RawMessage `json:"user,omitempty"`
	Provider       string          `json:"provider,omitempty"`
	ArtifactFileID string          `json:"artifact_file_id,omitempty"`
	ArtifactURL    string          `json:"artifact_url,omitempty"`
	UpstreamJobID  string          `json:"upstream_job_id,omitempty"`
	// WebhookURL is accepted so it never leaks upstream as an unknown field;
	// Lazu does not deliver video webhooks.
	WebhookURL string                     `json:"webhook_url,omitempty"`
	Extra      map[string]json.RawMessage `json:"-"`
}

func (r *VideoRequest) UnmarshalJSON(data []byte) error {
	var rawMap map[string]json.RawMessage
	if err := common.Unmarshal(data, &rawMap); err != nil {
		return err
	}
	knownFields := GetJSONFieldNames(reflect.TypeOf(*r))
	type Alias VideoRequest
	var known Alias
	if err := common.Unmarshal(data, &known); err != nil {
		return err
	}
	*r = VideoRequest(known)
	r.Extra = make(map[string]json.RawMessage)
	for key, value := range rawMap {
		if _, ok := knownFields[key]; !ok {
			r.Extra[key] = value
		}
	}
	return nil
}

func (r *VideoRequest) GetTokenCountMeta() *types.TokenCountMeta {
	return &types.TokenCountMeta{
		CombineText: r.Prompt,
	}
}

func (r *VideoRequest) IsStream(c *gin.Context) bool {
	return false
}

func (r *VideoRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

type OpenAIVideoRequest struct {
	Model          string          `json:"model"`
	Prompt         string          `json:"prompt"`
	Seconds        int             `json:"seconds,omitempty"`
	Size           string          `json:"size,omitempty"`
	AspectRatio    string          `json:"aspect_ratio,omitempty"`
	Quality        string          `json:"quality,omitempty"`
	ResponseFormat string          `json:"response_format,omitempty"`
	User           json.RawMessage `json:"user,omitempty"`
}

type GeminiVideoRequest struct {
	Instances  []GeminiVideoInstance `json:"instances"`
	Parameters GeminiVideoParameters `json:"parameters,omitempty"`
}

type GeminiVideoInstance struct {
	Prompt string `json:"prompt"`
}

type GeminiVideoParameters struct {
	DurationSeconds int    `json:"durationSeconds,omitempty"`
	AspectRatio     string `json:"aspectRatio,omitempty"`
	SampleCount     int    `json:"sampleCount,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
}
