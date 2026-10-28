package dto

import (
	"encoding/json"
	"strings"

	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// EmbeddingRequest is the OpenAI /v1/embeddings body. Input is kept as sent
// (a string, a list of strings, or token arrays) and forwarded unchanged to
// OpenAI-compatible upstreams; ParseInput reads the text out of it for the
// local token estimate and for the Gemini conversion.
type EmbeddingRequest struct {
	Model          string `json:"model"`
	Input          any    `json:"input"`
	EncodingFormat string `json:"encoding_format,omitempty"`
	Dimensions     int    `json:"dimensions,omitempty"`
	User           string `json:"user,omitempty"`
}

func (r *EmbeddingRequest) GetTokenCountMeta() *types.TokenCountMeta {
	var texts = make([]string, 0)

	inputs := r.ParseInput()
	for _, input := range inputs {
		texts = append(texts, input)
	}

	return &types.TokenCountMeta{
		CombineText:  strings.Join(texts, "\n"),
		TokenIDCount: r.countTokenIDs(),
	}
}

// countTokenIDs counts the input sent as token ids: one array of ids, or a
// list of them. Each id is one token, so an upstream that reports no usage
// still bills them instead of reading them as empty text.
func (r *EmbeddingRequest) countTokenIDs() int {
	items, ok := r.Input.([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, item := range items {
		switch value := item.(type) {
		case []any:
			for _, id := range value {
				if isTokenID(id) {
					count++
				}
			}
		default:
			if isTokenID(value) {
				count++
			}
		}
	}
	return count
}

func isTokenID(value any) bool {
	switch value.(type) {
	case float64, int, int64, json.Number:
		return true
	}
	return false
}

func (r *EmbeddingRequest) IsStream(c *gin.Context) bool {
	return false
}

func (r *EmbeddingRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

func (r *EmbeddingRequest) ParseInput() []string {
	if r.Input == nil {
		return make([]string, 0)
	}
	var input []string
	switch r.Input.(type) {
	case string:
		input = []string{r.Input.(string)}
	case []any:
		input = make([]string, 0, len(r.Input.([]any)))
		for _, item := range r.Input.([]any) {
			if str, ok := item.(string); ok {
				input = append(input, str)
			}
		}
	}
	return input
}
