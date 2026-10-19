package middleware

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// TextgenParse is the only protocol DTO/IR decode point for Chat and
// Responses routes. Downstream routing and relay handlers reuse the immutable
// ParsedRequest stored in context.
func TextgenParse(family textgen.ProtocolFamily) gin.HandlerFunc {
	return func(c *gin.Context) {
		parseTimer := common.StartHotPathStage(common.HotPathStageProtocolParse)
		body, err := common.GetRequestBody(c)
		if err != nil {
			parseTimer.Finish(err)
			abortRequestBodyError(c, err)
			return
		}
		var parsed *textgen.ParsedRequest
		switch family {
		case textgen.ProtocolOpenAIChat:
			semantic, wire, decodeErr := openai_chat.Decode(body)
			if decodeErr != nil {
				parseTimer.Finish(decodeErr)
				abortWithCode(c, types.ErrorCodeInvalidRequest, types.D{"reason": decodeErr.Error()})
				return
			}
			features, uses := textgen.ExtractFeatures(semantic)
			parsed = &textgen.ParsedRequest{Client: textgen.Protocol{Family: family, Dialect: "openai"}, Semantic: semantic, Wire: wire, Features: features, Uses: uses}
		case textgen.ProtocolOpenAIResponses:
			semantic, wire, decodeErr := openai_responses.Decode(body)
			if decodeErr != nil {
				parseTimer.Finish(decodeErr)
				abortWithCode(c, types.ErrorCodeInvalidRequest, types.D{"reason": decodeErr.Error()})
				return
			}
			features, uses := textgen.ExtractFeatures(semantic)
			parsed = &textgen.ParsedRequest{Client: textgen.Protocol{Family: family, Dialect: "openai"}, Semantic: semantic, Wire: wire, Features: features, Uses: uses}
		default:
			abortWithCode(c, types.ErrorCodeEndpointNotSupported)
			return
		}
		parseTimer.Finish(nil)
		common.SetContextKey(c, constant.ContextKeyTextgenParsed, parsed)
		c.Next()
	}
}
