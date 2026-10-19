package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestAdmissionComparisonLabelsAndPreservesEstimate(t *testing.T) {
	previous, previousNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	defer func() { constant.GetMediaToken, constant.GetMediaTokenNotStream = previous, previousNonStream }()
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	service.InitTokenEncoders()
	for _, mode := range []string{"text", "disabled", "non_stream_disabled", "media"} {
		t.Run(mode, func(t *testing.T) {
			constant.GetMediaToken = mode != "disabled"
			constant.GetMediaTokenNotStream = mode != "non_stream_disabled"
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyPromptTokens, 777)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
			request := &textgen.Request{Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}}}
			if mode == "media" {
				request.Conversation[0].Content = append(request.Conversation[0].Content, textgen.ContentPart{Kind: textgen.ContentImage, URL: "https://example.invalid/not-fetched"})
			}
			info := &relaycommon.RelayInfo{ProtocolRequest: request, RelayFormat: types.RelayFormatOpenAI, PromptTokens: 777}
			recordProtocolAdmissionComparison(c, info, 777)
			if info.PromptTokens != 777 || common.GetContextKeyInt(c, constant.ContextKeyPromptTokens) != 777 {
				t.Fatal("telemetry changed admission/billing state")
			}
			_, present := common.GetContextKeyType[int](c, constant.ContextKeyProtocolLocalTokens)
			if mode != "media" {
				if present {
					t.Fatal("shadow tokenization ran synchronously")
				}
				if _, exists := c.Get("protocol_token_shadow_meta"); !exists {
					t.Fatal("missing deferred immutable token metadata")
				}
				if common.GetContextKeyString(c, constant.ContextKeyProtocolTokenSkip) != "" {
					t.Fatal("legacy counting switch skipped background shadow work")
				}
			} else if present || common.GetContextKeyString(c, constant.ContextKeyProtocolTokenSkip) == "" {
				t.Fatal("skipped comparison emitted a misleading zero")
			}
			if _, exists := c.Get("protocol_legacy_prompt_tokens"); exists {
				t.Fatal("estimate labeled legacy exact")
			}
		})
	}
}
