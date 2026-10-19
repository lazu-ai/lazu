package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"gorm.io/gorm"
)

func TestBuildTextBillingQuoteAnnotatesUsageCostAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:text_billing_quote?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	if err := db.AutoMigrate(&model.ModelSellPrice{}); err != nil {
		t.Fatalf("failed to migrate pricing tables: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	}()

	inputPrice := 2.0
	outputPrice := 4.0
	if err := db.Create(&model.ModelSellPrice{
		ModelName:      "cost-model",
		InputPerMtok:   &inputPrice,
		OutputPerMtok:  &outputPrice,
		Active:         true,
		PricingVersion: 9,
	}).Error; err != nil {
		t.Fatalf("failed to insert sell price: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(common.RequestIdKey, "req-cost-1")

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}
	quote := BuildTextBillingQuote(c, &relaycommon.RelayInfo{
		UserId:          1,
		OriginModelName: "cost-model",
	}, usage, "")
	AnnotateUsageCost(c, usage, quote.Quote)

	if quote.Quote.TotalMicrousd != 4000 {
		t.Fatalf("expected 4000 microusd quote, got %d", quote.Quote.TotalMicrousd)
	}
	if usage.Cost != 0.004 {
		t.Fatalf("expected usage cost 0.004, got %#v", usage.Cost)
	}
	if got := w.Header().Get("x-lazu-cost-usd"); got != "0.004000" {
		t.Fatalf("expected cost header 0.004000, got %q", got)
	}
	if got := w.Header().Get("x-lazu-request-id"); got != "req-cost-1" {
		t.Fatalf("expected request id header, got %q", got)
	}
}

func TestResponsesWebSearchToolInfoSupportsWebSearchAndPreview(t *testing.T) {
	webSearchInfo := &relaycommon.RelayInfo{
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearch: {
					CallCount:         2,
					SearchContextSize: "high",
				},
				dto.BuildInToolWebSearchPreview: {
					CallCount:         1,
					SearchContextSize: "low",
				},
			},
		},
	}
	if got := ResponsesWebSearchToolInfo(webSearchInfo); got == nil || got.CallCount != 2 || got.SearchContextSize != "high" {
		t.Fatalf("expected web_search tool to be preferred, got %+v", got)
	}

	previewInfo := &relaycommon.RelayInfo{
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {
					CallCount:         1,
					SearchContextSize: "low",
				},
			},
		},
	}
	if got := ResponsesWebSearchToolInfo(previewInfo); got == nil || got.CallCount != 1 || got.SearchContextSize != "low" {
		t.Fatalf("expected web_search_preview fallback, got %+v", got)
	}
}

func TestMissingUsageDoesNotSettleAdmissionOrPerCallReservation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, perCall := range []bool{false, true} {
		info := &relaycommon.RelayInfo{PromptTokens: 99999, FinalPreConsumedQuota: 12345}
		info.PriceData.UsePrice = perCall
		info.PriceData.PerCallPriceUSD = 5
		quote := BuildTextBillingQuote(c, info, nil, "")
		if quote.Quota != 0 || quote.Usage.TotalTokens != 0 || quote.Usage.Measurement().Input != dto.UsageSourceUnavailable {
			t.Fatalf("missing usage charged: %+v", quote)
		}
		if info.FinalPreConsumedQuota != 12345 {
			t.Fatal("quote prematurely released reservation")
		}
	}
}

func TestSeparatelyPricedAudioCountsTowardContextTier(t *testing.T) {
	db := setupModelInfoTestDB(t, "audio_context_threshold")
	if err := db.Create(&model.ModelSellPrice{
		ModelName: "gemini-2.5-flash", ChannelGroup: model.ChannelLaneDirect, Active: true,
		InputPerMtok: float(2), OutputPerMtok: float(10), AudioInputPerMtok: float(99),
		ContextTierThreshold: 100, ContextTierInputPerMtok: float(4), ContextTierOutputPerMtok: float(20),
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, audio := range []int{40, 41} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		usage := &dto.Usage{PromptTokens: 60 + audio, CompletionTokens: 10, PromptTokensDetails: dto.InputTokenDetails{AudioTokens: audio}}
		result := BuildTextBillingQuote(c, &relaycommon.RelayInfo{OriginModelName: "gemini-2.5-flash", UsingGroup: model.ChannelLaneDirect}, usage, "")
		want := int64(60*2 + 10*10 + audio)
		if audio == 41 {
			want = 60*4 + 10*20 + int64(audio)
			if result.Quote.ContextTier == nil || result.Quote.ContextTier.InputTokens != 101 {
				t.Fatalf("audio omitted from tier: %+v", result.Quote.ContextTier)
			}
		} else if result.Quote.ContextTier != nil {
			t.Fatal("threshold equality must use base rates")
		}
		if int64(result.Quota) != want {
			t.Fatalf("audio=%d: total %d, want %d", audio, result.Quota, want)
		}
		if result.BillingUsage.AudioInputTokens != 0 {
			t.Fatal("separate audio charged twice")
		}
		for _, item := range result.Quote.LineItems {
			if item.Dimension == "audio_input" {
				t.Fatal("ordinary audio line must be absent")
			}
		}
	}
}
