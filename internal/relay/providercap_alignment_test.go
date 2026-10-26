package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

func newTestRelayInfo(apiType int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		IsStream:        false,
		OriginModelName: "test-model",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           apiType,
			UpstreamModelName: "test-model",
		},
	}
}

func newTestGinContext() *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", nil)
	return c
}

func isCapabilityStubError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not implemented") ||
		strings.Contains(lower, "not available") ||
		strings.Contains(lower, "endpoint not supported") ||
		strings.Contains(lower, "not supported") ||
		strings.Contains(lower, "implement me")
}

func invokeConvertClaudeRequest(converter interface {
	ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error)
}, c *gin.Context, info *relaycommon.RelayInfo) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ConvertClaudeRequest: %v", r)
		}
	}()
	_, err = converter.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{
		Model:     "test-model",
		MaxTokens: 16,
		Messages: []dto.ClaudeMessage{
			{
				Role:    "user",
				Content: "hello",
			},
		},
	})
	return err
}

func invokeConvertResponsesRequest(converter interface {
	ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error)
}, c *gin.Context, info *relaycommon.RelayInfo) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ConvertOpenAIResponsesRequest: %v", r)
		}
	}()
	_, err = converter.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model: "test-model",
		Input: json.RawMessage(`"hello"`),
	})
	return err
}

func invokeConvertGeminiRequest(converter interface {
	ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error)
}, c *gin.Context, info *relaycommon.RelayInfo) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in ConvertGeminiRequest: %v", r)
		}
	}()
	_, err = converter.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{Text: "hello"},
				},
			},
		},
	})
	return err
}

// A provider that declares Claude client support serves it one of two ways: a
// Claude-family provider converts the request itself (natively, or with the
// identity converter); any other is bridged by the protocol hub to Chat, so
// it must accept an OpenAI Chat request.
func TestProviderCap_DeclaredClaudeSupportHasAPath(t *testing.T) {
	t.Parallel()

	apiTypes := make([]int, 0, len(adaptorFactories))
	for apiType := range adaptorFactories {
		apiTypes = append(apiTypes, apiType)
	}
	sort.Ints(apiTypes)

	for _, apiType := range apiTypes {
		apiType := apiType
		t.Run(fmt.Sprintf("api_type_%d", apiType), func(t *testing.T) {
			t.Parallel()

			supported, _ := providercap.IsSupported(apiType, types.RelayFormatClaude)
			if !supported {
				return
			}

			adaptor := GetAdaptor(apiType)
			if adaptor == nil {
				t.Fatalf("expected adaptor for apiType=%d", apiType)
			}
			info := newTestRelayInfo(apiType)
			adaptor.Init(info)

			if providercap.ProtocolFamilyOf(apiType) == providercap.ProtocolFamilyClaude {
				converter, lazuErr := requireClaudeRequestConverter(adaptor, info)
				if lazuErr != nil {
					t.Fatalf("declared claude support but converter assertion failed: %v", lazuErr)
				}
				if err := invokeConvertClaudeRequest(converter, newTestGinContext(), info); isCapabilityStubError(err) {
					t.Fatalf("declared claude support but converter is stub-like: %v", err)
				}
				return
			}
			if _, lazuErr := requireOpenAIRequestConverter(adaptor, info); lazuErr != nil {
				t.Fatalf("declared claude support is bridged through Chat, but the adaptor takes no Chat request: %v", lazuErr)
			}
		})
	}
}

func TestProviderCap_DeclaredResponsesSupportHasUsableConverter(t *testing.T) {
	t.Parallel()

	apiTypes := make([]int, 0, len(adaptorFactories))
	for apiType := range adaptorFactories {
		apiTypes = append(apiTypes, apiType)
	}
	sort.Ints(apiTypes)

	for _, apiType := range apiTypes {
		apiType := apiType
		t.Run(fmt.Sprintf("api_type_%d", apiType), func(t *testing.T) {
			t.Parallel()

			supported, _ := providercap.IsSupported(apiType, types.RelayFormatOpenAIResponses)
			if !supported {
				return
			}

			adaptor := GetAdaptor(apiType)
			if adaptor == nil {
				t.Fatalf("expected adaptor for apiType=%d", apiType)
			}
			info := newTestRelayInfo(apiType)
			adaptor.Init(info)

			converter, lazuErr := requireResponsesRequestConverter(adaptor, info)
			if lazuErr != nil {
				t.Fatalf("declared responses support but converter assertion failed: %v", lazuErr)
			}

			err := invokeConvertResponsesRequest(converter, newTestGinContext(), info)
			if isCapabilityStubError(err) {
				t.Fatalf("declared responses support but converter is stub-like: %v", err)
			}
		})
	}
}

func TestProviderCap_DeclaredRealtimeSupportHasUsableBaseAdaptor(t *testing.T) {
	t.Parallel()

	apiTypes := make([]int, 0, len(adaptorFactories))
	for apiType := range adaptorFactories {
		apiTypes = append(apiTypes, apiType)
	}
	sort.Ints(apiTypes)

	for _, apiType := range apiTypes {
		apiType := apiType
		t.Run(fmt.Sprintf("api_type_%d", apiType), func(t *testing.T) {
			t.Parallel()

			supported, _ := providercap.IsSupported(apiType, types.RelayFormatOpenAIRealtime)
			if !supported {
				return
			}

			adaptor := GetAdaptor(apiType)
			if adaptor == nil {
				t.Fatalf("expected adaptor for apiType=%d", apiType)
			}
			info := newTestRelayInfo(apiType)
			info.ChannelBaseUrl = "https://example.com"
			info.RequestURLPath = "/v1/realtime"
			info.RelayMode = relayconstant.RelayModeRealtime
			adaptor.Init(info)

			requestURL, err := adaptor.GetRequestURL(info)
			if err != nil {
				t.Fatalf("declared realtime support but GetRequestURL failed: %v", err)
			}
			if !strings.HasPrefix(requestURL, "wss://") && !strings.HasPrefix(requestURL, "ws://") {
				t.Fatalf("declared realtime support but request url is not websocket: %s", requestURL)
			}

			header := http.Header{}
			if err := adaptor.SetupRequestHeader(newTestGinContext(), &header, info); err != nil {
				t.Fatalf("declared realtime support but SetupRequestHeader failed: %v", err)
			}
		})
	}
}

func TestProviderCap_DeclaredGeminiSupportHasUsableConverter(t *testing.T) {
	t.Parallel()

	apiTypes := make([]int, 0, len(adaptorFactories))
	for apiType := range adaptorFactories {
		apiTypes = append(apiTypes, apiType)
	}
	sort.Ints(apiTypes)

	for _, apiType := range apiTypes {
		apiType := apiType
		t.Run(fmt.Sprintf("api_type_%d", apiType), func(t *testing.T) {
			t.Parallel()

			supported, _ := providercap.IsSupported(apiType, types.RelayFormatGemini)
			if !supported {
				return
			}

			adaptor := GetAdaptor(apiType)
			if adaptor == nil {
				t.Fatalf("expected adaptor for apiType=%d", apiType)
			}
			info := newTestRelayInfo(apiType)
			adaptor.Init(info)

			converter, lazuErr := requireGeminiRequestConverter(adaptor, info)
			if lazuErr != nil {
				t.Fatalf("declared gemini support but converter assertion failed: %v", lazuErr)
			}

			err := invokeConvertGeminiRequest(converter, newTestGinContext(), info)
			if isCapabilityStubError(err) {
				t.Fatalf("declared gemini support but converter is stub-like: %v", err)
			}
		})
	}
}

func TestProviderCap_UnsupportedFormatsDoNotExposeStubConverters(t *testing.T) {
	t.Parallel()

	for apiType, factory := range adaptorFactories {
		apiType := apiType
		factory := factory
		t.Run(fmt.Sprintf("api_type_%d", apiType), func(t *testing.T) {
			t.Parallel()

			adaptor := factory()
			profile := providercap.GetProfile(apiType)
			if !profile.SupportsClaude {
				if _, ok := adaptor.(channel.ClaudeRequestConverter); ok {
					t.Fatal("unsupported claude format must be absent from the adaptor interface")
				}
			}
			if !profile.SupportsGemini {
				if _, ok := adaptor.(channel.GeminiRequestConverter); ok {
					t.Fatal("unsupported gemini format must be absent from the adaptor interface")
				}
			}
			if !profile.SupportsResponses {
				if _, ok := adaptor.(channel.ResponsesRequestConverter); ok {
					t.Fatal("unsupported responses format must be absent from the adaptor interface")
				}
			}
		})
	}
}
