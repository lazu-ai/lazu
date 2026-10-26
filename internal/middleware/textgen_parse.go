package middleware

import (
	"errors"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// TextgenParse is the protocol decode point for Chat and Responses routes
// (textgenruntime.Parse). Downstream routing and relay handlers reuse the
// immutable ParsedRequest stored in context.
func TextgenParse(family textgen.ProtocolFamily) gin.HandlerFunc {
	return func(c *gin.Context) {
		parseTimer := common.StartHotPathStage(common.HotPathStageProtocolParse)
		body, err := common.GetRequestBody(c)
		if err != nil {
			parseTimer.Finish(err)
			abortRequestBodyError(c, err)
			return
		}
		parsed, decodeErr := textgenruntime.Parse(family, body)
		if decodeErr != nil {
			parseTimer.Finish(decodeErr)
			if errors.Is(decodeErr, textgenruntime.ErrNoDecoder) {
				abortWithCode(c, types.ErrorCodeEndpointNotSupported)
				return
			}
			abortWithCode(c, types.ErrorCodeInvalidRequest, types.D{"reason": decodeErr.Error()})
			return
		}
		parseTimer.Finish(nil)
		common.SetContextKey(c, constant.ContextKeyTextgenParsed, parsed)
		c.Next()
	}
}

// ClaudeClientParse is TextgenParse for Claude clients on /v1/messages.
func ClaudeClientParse() gin.HandlerFunc {
	return TextgenParse(textgen.ProtocolAnthropic)
}

// GeminiClientParse is TextgenParse for Gemini clients on
// /v1beta/models/{model}:generateContent (and its /v1 and streaming forms).
// The same route carries embeddings and other actions, which pass through
// unparsed.
func GeminiClientParse() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		colon := strings.LastIndex(path, ":")
		if colon < 0 || !strings.Contains(path, "/models/") {
			c.Next()
			return
		}
		action := path[colon+1:]
		if action != "generateContent" && action != "streamGenerateContent" {
			c.Next()
			return
		}
		parseTimer := common.StartHotPathStage(common.HotPathStageProtocolParse)
		body, err := common.GetRequestBody(c)
		if err != nil {
			parseTimer.Finish(err)
			abortRequestBodyError(c, err)
			return
		}
		parsed, decodeErr := textgenruntime.ParseGemini(body, extractModelNameFromGeminiPath(path), dto.IsGeminiStreamRequest(c))
		if decodeErr != nil {
			parseTimer.Finish(decodeErr)
			abortWithCode(c, types.ErrorCodeInvalidRequest, types.D{"reason": decodeErr.Error()})
			return
		}
		parseTimer.Finish(nil)
		common.SetContextKey(c, constant.ContextKeyTextgenParsed, parsed)
		c.Next()
	}
}
