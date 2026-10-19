package helper

import (
	"math"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestParsedTextgenRequestsStillRunLegacyTokenValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		path   string
		format types.RelayFormat
		parsed *textgen.ParsedRequest
	}{
		{
			name:   "chat max completion tokens",
			path:   "/v1/chat/completions",
			format: types.RelayFormatOpenAI,
			parsed: &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIChat}, Wire: &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "hello"}}, MaxCompletionTokens: uint(math.MaxInt32/2 + 1)}},
		},
		{
			name:   "responses max output tokens",
			path:   "/v1/responses",
			format: types.RelayFormatOpenAIResponses,
			parsed: &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}, Wire: &dto.OpenAIResponsesRequest{Model: "m", Input: []byte(`"hello"`), MaxOutputTokens: uint(math.MaxInt32/2 + 1)}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", test.path, nil)
			common.SetContextKey(c, constant.ContextKeyTextgenParsed, test.parsed)
			if _, err := GetAndValidateRequest(c, test.format); err == nil {
				t.Fatal("oversized parsed request should fail validation")
			}
		})
	}
}
