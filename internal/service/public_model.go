package service

import (
	"bytes"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// publicModelPaths are where the response shapes the gateway serves carry
// the answering model: OpenAI Chat, Completions, Embeddings and Responses
// bodies ("model"), Responses stream events ("response.model"), Claude's
// message_start ("message.model") and Gemini replies ("modelVersion").
var publicModelPaths = []string{"model", "response.model", "message.model", "modelVersion"}

// RestorePublicModel replaces the upstream model id in a JSON response body
// or stream frame with the name the caller asked for, when the channel
// mapped that name to another one. Anything that is not a JSON object, and
// every request served without a mapping, passes through untouched.
func RestorePublicModel(c *gin.Context, data []byte) []byte {
	if c == nil {
		return data
	}
	public := common.GetContextKeyString(c, constant.ContextKeyPublicModel)
	if public == "" {
		return data
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return data
	}
	for _, path := range publicModelPaths {
		value := gjson.GetBytes(data, path)
		if value.Type != gjson.String || value.Str == public {
			continue
		}
		if rewritten, err := sjson.SetBytes(data, path, public); err == nil {
			data = rewritten
		}
	}
	return data
}

// RestorePublicModelString is RestorePublicModel for a stream frame held as
// a string.
func RestorePublicModelString(c *gin.Context, data string) string {
	if c == nil || common.GetContextKeyString(c, constant.ContextKeyPublicModel) == "" {
		return data
	}
	return string(RestorePublicModel(c, []byte(data)))
}
