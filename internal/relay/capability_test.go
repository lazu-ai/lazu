package relay

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

type baseOnlyAdaptor struct{}

func (a *baseOnlyAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *baseOnlyAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return "", nil
}

func (a *baseOnlyAdaptor) SetupRequestHeader(_ *gin.Context, _ *http.Header, _ *relaycommon.RelayInfo) error {
	return nil
}

func (a *baseOnlyAdaptor) DoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ io.Reader) (any, error) {
	return nil, nil
}

func (a *baseOnlyAdaptor) DoResponse(_ *gin.Context, _ *http.Response, _ *relaycommon.RelayInfo) (any, *types.LazuError) {
	return nil, nil
}

func (a *baseOnlyAdaptor) GetModelList() []string {
	return nil
}

func (a *baseOnlyAdaptor) GetChannelName() string {
	return "test"
}

type allCapabilityAdaptor struct {
	baseOnlyAdaptor
}

func (a *allCapabilityAdaptor) ConvertOpenAIRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.GeneralOpenAIRequest) (any, error) {
	return nil, nil
}

func (a *allCapabilityAdaptor) ConvertOpenAIResponsesRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.OpenAIResponsesRequest) (any, error) {
	return nil, nil
}

func (a *allCapabilityAdaptor) ConvertClaudeRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.ClaudeRequest) (any, error) {
	return nil, nil
}

func (a *allCapabilityAdaptor) ConvertGeminiRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.GeminiChatRequest) (any, error) {
	return nil, nil
}

func (a *allCapabilityAdaptor) ConvertImageRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.ImageRequest) (any, error) {
	return nil, nil
}

func (a *allCapabilityAdaptor) ConvertAudioRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.AudioRequest) (io.Reader, error) {
	return strings.NewReader(""), nil
}

func (a *allCapabilityAdaptor) ConvertEmbeddingRequest(_ *gin.Context, _ *relaycommon.RelayInfo, _ dto.EmbeddingRequest) (any, error) {
	return nil, nil
}

func (a *allCapabilityAdaptor) ConvertRerankRequest(_ *gin.Context, _ int, _ dto.RerankRequest) (any, error) {
	return nil, nil
}

func TestRequireConverters_ReturnsCapabilityErrorWhenMissing(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiType: 321}}
	adaptor := &baseOnlyAdaptor{}

	checkMissing := func(name, wantCapability string, gotErr *types.LazuError) {
		t.Helper()
		if gotErr == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
		if gotErr.GetErrorCode() != types.ErrorCodeInvalidApiType {
			t.Fatalf("%s: unexpected error code: %s", name, gotErr.GetErrorCode())
		}
		if !types.IsSkipRetryError(gotErr) {
			t.Fatalf("%s: expected skip-retry error", name)
		}
		if !strings.Contains(gotErr.Error(), wantCapability) {
			t.Fatalf("%s: expected error message to contain %q, got %q", name, wantCapability, gotErr.Error())
		}
	}

	_, err := requireOpenAIRequestConverter(adaptor, info)
	checkMissing("openai", "openai_request_converter", err)

	_, err = requireResponsesRequestConverter(adaptor, info)
	checkMissing("responses", "responses_request_converter", err)

	_, err = requireClaudeRequestConverter(adaptor, info)
	checkMissing("claude", "claude_request_converter", err)

	_, err = requireGeminiRequestConverter(adaptor, info)
	checkMissing("gemini", "gemini_request_converter", err)

	_, err = requireImageRequestConverter(adaptor, info)
	checkMissing("image", "image_request_converter", err)

	_, err = requireAudioRequestConverter(adaptor, info)
	checkMissing("audio", "audio_request_converter", err)

	_, err = requireEmbeddingRequestConverter(adaptor, info)
	checkMissing("embedding", "embedding_request_converter", err)

	_, err = requireRerankRequestConverter(adaptor, info)
	checkMissing("rerank", "rerank_request_converter", err)
}

func TestRequireConverters_ReturnsConvertersWhenSupported(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiType: 123}}
	adaptor := &allCapabilityAdaptor{}

	if _, err := requireOpenAIRequestConverter(adaptor, info); err != nil {
		t.Fatalf("openai converter: unexpected error: %v", err)
	}
	if _, err := requireResponsesRequestConverter(adaptor, info); err != nil {
		t.Fatalf("responses converter: unexpected error: %v", err)
	}
	if _, err := requireClaudeRequestConverter(adaptor, info); err != nil {
		t.Fatalf("claude converter: unexpected error: %v", err)
	}
	if _, err := requireGeminiRequestConverter(adaptor, info); err != nil {
		t.Fatalf("gemini converter: unexpected error: %v", err)
	}
	if _, err := requireImageRequestConverter(adaptor, info); err != nil {
		t.Fatalf("image converter: unexpected error: %v", err)
	}
	if _, err := requireAudioRequestConverter(adaptor, info); err != nil {
		t.Fatalf("audio converter: unexpected error: %v", err)
	}
	if _, err := requireEmbeddingRequestConverter(adaptor, info); err != nil {
		t.Fatalf("embedding converter: unexpected error: %v", err)
	}
	if _, err := requireRerankRequestConverter(adaptor, info); err != nil {
		t.Fatalf("rerank converter: unexpected error: %v", err)
	}
}

func TestRequireConverters_NilChannelMetaIsSafe(t *testing.T) {
	t.Parallel()

	_, err := requireOpenAIRequestConverter(&baseOnlyAdaptor{}, &relaycommon.RelayInfo{})
	if err == nil {
		t.Fatal("expected error for missing converter")
	}
	if !strings.Contains(err.Error(), "api type -1") {
		t.Fatalf("expected fallback api type -1, got: %q", err.Error())
	}
}

func TestRequireConverters_ProtocolFamilyFallback(t *testing.T) {
	t.Parallel()

	base := &baseOnlyAdaptor{}

	openAIInfo := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI}}
	if _, err := requireOpenAIRequestConverter(base, openAIInfo); err != nil {
		t.Fatalf("expected openai family fallback converter, got error: %v", err)
	}
	if _, err := requireResponsesRequestConverter(base, openAIInfo); err != nil {
		t.Fatalf("expected responses fallback converter for openai profile, got error: %v", err)
	}

	claudeInfo := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeAnthropic}}
	if _, err := requireClaudeRequestConverter(base, claudeInfo); err != nil {
		t.Fatalf("expected claude family fallback converter, got error: %v", err)
	}

	geminiInfo := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeGemini}}
	if _, err := requireGeminiRequestConverter(base, geminiInfo); err != nil {
		t.Fatalf("expected gemini family fallback converter, got error: %v", err)
	}

	if _, err := requireResponsesRequestConverter(base, geminiInfo); err == nil {
		t.Fatal("expected responses converter to be blocked for gemini profile")
	}
}

var _ channel.BaseAdaptor = (*baseOnlyAdaptor)(nil)
var _ channel.OpenAIRequestConverter = (*allCapabilityAdaptor)(nil)
var _ channel.ResponsesRequestConverter = (*allCapabilityAdaptor)(nil)
var _ channel.ClaudeRequestConverter = (*allCapabilityAdaptor)(nil)
var _ channel.GeminiRequestConverter = (*allCapabilityAdaptor)(nil)
var _ channel.ImageRequestConverter = (*allCapabilityAdaptor)(nil)
var _ channel.AudioRequestConverter = (*allCapabilityAdaptor)(nil)
var _ channel.EmbeddingRequestConverter = (*allCapabilityAdaptor)(nil)
var _ channel.RerankRequestConverter = (*allCapabilityAdaptor)(nil)
