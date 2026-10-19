package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestEstimateRequestTPMTokensUsesBoundedMediaReserve(t *testing.T) {
	meta := &types.TokenCountMeta{
		CombineText:   "hello",
		MessagesCount: 1,
		ToolsCount:    1,
		Files:         []*types.FileMeta{{FileType: types.FileTypeImage, OriginData: strings.Repeat("A", 8<<20)}, {FileType: types.FileTypeImage, OriginData: "small"}},
	}
	info := &relaycommon.RelayInfo{OriginModelName: "k2.6", RelayFormat: types.RelayFormatOpenAI}
	got, err := EstimateRequestTPMTokens(meta, info)
	if err != nil {
		t.Fatal(err)
	}
	want := estimateTextBytes(meta.CombineText) + 1*8 + 1*3 + 3 + 2*k26ImageTPMReserve
	if got != want {
		t.Fatalf("estimate=%d, want=%d", got, want)
	}
}

func TestEstimateRequestTPMTokensDoesNotDependOnImagePayloadSize(t *testing.T) {
	info := &relaycommon.RelayInfo{OriginModelName: "k2.6"}
	small, err := EstimateRequestTPMTokens(&types.TokenCountMeta{Files: []*types.FileMeta{{FileType: types.FileTypeImage, OriginData: "small"}}}, info)
	if err != nil {
		t.Fatal(err)
	}
	large, err := EstimateRequestTPMTokens(&types.TokenCountMeta{Files: []*types.FileMeta{{FileType: types.FileTypeImage, OriginData: strings.Repeat("B", 32<<20)}}}, info)
	if err != nil {
		t.Fatal(err)
	}
	if small != large {
		t.Fatalf("image payload size changed estimate: small=%d large=%d", small, large)
	}
}

func TestEstimateRequestTPMTokensPreservesTextNumberRuneSemantics(t *testing.T) {
	meta := &types.TokenCountMeta{TokenType: types.TokenTypeTextNumber, CombineText: "hello 世界"}
	got, err := EstimateRequestTPMTokens(meta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := len([]rune(meta.CombineText)); got != want {
		t.Fatalf("estimate=%d, want rune count=%d", got, want)
	}
}

func TestPromptTokensForUsageLazilyResolvesExactCount(t *testing.T) {
	previousMediaToken := constant.GetMediaToken
	previousMediaTokenNotStream := constant.GetMediaTokenNotStream
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	defer func() {
		constant.GetMediaToken = previousMediaToken
		constant.GetMediaTokenNotStream = previousMediaTokenNotStream
	}()

	info := &relaycommon.RelayInfo{
		PromptTokens: 2048,
		RelayFormat:  types.RelayFormatOpenAIAudio,
		Request:      &dto.AudioRequest{Input: "hello"},
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(constant.ContextKeyPromptTokens), 2048)
	if got := PromptTokensForUsage(c, info); got != 5 {
		t.Fatalf("exact prompt tokens=%d, want=5", got)
	}
	if info.PromptTokens != 2048 || !info.UsagePromptTokensSet || !info.UsagePromptTokensAvailable {
		t.Fatalf("exact count was not cached: %+v", info)
	}
	if c.GetInt(string(constant.ContextKeyPromptTokens)) != 2048 {
		t.Fatal("usage count changed admission context")
	}
}

func TestPromptUsageUnavailableNeverFallsBackToReservation(t *testing.T) {
	oldCount, oldNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	t.Cleanup(func() { constant.GetMediaToken, constant.GetMediaTokenNotStream = oldCount, oldNonStream })
	for _, tc := range []struct {
		name    string
		enabled bool
		request dto.Request
	}{
		{"missing request", true, nil},
		{"count disabled", false, &dto.AudioRequest{Input: "hello"}},
		{"remote image", true, &usageCountRequest{meta: &types.TokenCountMeta{Files: []*types.FileMeta{{FileType: types.FileTypeImage, OriginData: "https://unreachable.invalid/image.png"}}}}},
		{"unknown media", true, &usageCountRequest{meta: &types.TokenCountMeta{Files: []*types.FileMeta{{FileType: types.FileTypeVideo, OriginData: "secret"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constant.GetMediaToken, constant.GetMediaTokenNotStream = tc.enabled, true
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(string(constant.ContextKeyPromptTokens), 9000)
			info := &relaycommon.RelayInfo{PromptTokens: 9000, OriginModelName: "gpt-4o-mini", Request: tc.request}
			usage := &dto.Usage{CompletionTokens: 7}
			ApplyLocalPromptUsage(c, info, usage)
			if usage.PromptTokens != 0 || usage.InputTokenSource != dto.UsageSourceUnavailable || info.UsagePromptTokensAvailable {
				t.Fatalf("reservation became usage: %+v", usage)
			}
			if info.PromptTokens != 9000 || c.GetInt(string(constant.ContextKeyPromptTokens)) != 9000 {
				t.Fatal("admission mutated")
			}
		})
	}
}

type usageCountRequest struct {
	dto.BaseRequest
	meta *types.TokenCountMeta
}

func (r *usageCountRequest) GetTokenCountMeta() *types.TokenCountMeta { return r.meta }
