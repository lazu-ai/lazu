package middleware

import (
	"errors"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
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

// ClaudeClientParse is TextgenParse for Claude clients on /v1/messages,
// unless the operator has switched them back to the legacy conversion
// (model_setting.GlobalSettings.LegacyClaudeClientConversion): then nothing
// is parsed and nothing planned, exactly as before the hub.
func ClaudeClientParse() gin.HandlerFunc {
	parse := TextgenParse(textgen.ProtocolAnthropic)
	return func(c *gin.Context) {
		if model_setting.GetGlobalSettings().LegacyClaudeClientConversion {
			c.Next()
			return
		}
		parse(c)
	}
}
