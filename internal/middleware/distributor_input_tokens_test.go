package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayrouter "github.com/lazu-ai/lazu/internal/relay/router"
	"github.com/lazu-ai/lazu/internal/service"
)

func TestSetStructuredInputTokenEstimateExcludesMediaAdmissionReserve(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := &textgen.Request{
		Conversation: []textgen.InputItem{{
			Role: "user",
			Content: []textgen.ContentPart{
				{Kind: textgen.ContentText, Text: "describe"},
				{Kind: textgen.ContentImage, Data: strings.Repeat("A", 16<<20)},
			},
		}},
	}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{
		Client:   textgen.Protocol{Family: textgen.ProtocolOpenAIResponses},
		Semantic: request,
	})

	setStructuredInputTokenEstimate(c, &relayrouter.RequestContext{}, "k2.6")
	parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
	if !ok || parsed == nil {
		t.Fatal("structured parsed request missing")
	}
	if parsed.InputTokens <= 0 || parsed.InputTokens >= 1000 {
		t.Fatalf("input token estimate=%d, want text-only context estimate", parsed.InputTokens)
	}
}

func TestKimiMultiImageContextDoesNotUseQuotaReservation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	parts := []textgen.ContentPart{{Kind: textgen.ContentText, Text: strings.Repeat("a", 53329)}}
	for range 120 {
		parts = append(parts, textgen.ContentPart{Kind: textgen.ContentImage, URL: "https://example.test/frame.jpg", Detail: "high"})
	}
	request := &textgen.Request{Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: parts}}}
	client := textgen.Protocol{Family: textgen.ProtocolOpenAIChat}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{Client: client, Semantic: request, InputTokens: 510188})
	setStructuredInputTokenEstimate(c, &relayrouter.RequestContext{}, "kimi-k2.6")
	parsed, _ := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
	reserve, err := service.EstimateRequestTPMTokens(textgen.TokenCountMeta(request), &relaycommon.RelayInfo{OriginModelName: "kimi-k2.6"})
	if err != nil || reserve < 120*4096 {
		t.Fatalf("media quota protection lost: reserve=%d err=%v", reserve, err)
	}
	var features textgen.FeatureSet
	features.Add(textgen.FeatureEndpointChat)
	features.Add(textgen.FeatureInputText)
	features.Add(textgen.FeatureInputImage)
	capabilities := textgen.CapabilitySnapshot{Dialect: "openai_chat_compatible", Features: features, Constraints: map[textgen.FeatureID]textgen.Constraint{textgen.FeatureInputText: {Max: 262144}}}
	plan, err := textgen.PlanAttempt(client, capabilities, request, 66, "kimi-k2.6", parsed.InputTokens)
	if err != nil || plan == nil {
		t.Fatalf("valid multi-image request rejected by quota estimate: %v", err)
	}
	if len(textgen.TokenCountMeta(request).Files) != 120 {
		t.Fatal("context estimate changed request images")
	}
}
