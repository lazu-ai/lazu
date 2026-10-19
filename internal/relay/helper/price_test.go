package helper

import (
	"github.com/lazu-ai/lazu/pkg/billing"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

func TestReservationOmitsOutputForNonGenerativeRequests(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	defer func() { sqlDB, _ := db.DB(); _ = sqlDB.Close(); model.SetCoreDB(previousDB) }()
	if err := db.AutoMigrate(&model.ModelSellPrice{}); err != nil {
		t.Fatal(err)
	}
	input, output := 1.0, 10.0
	if err := db.Create(&model.ModelSellPrice{ModelName: "reserve-kind-test", ChannelGroup: "direct", InputPerMtok: &input, OutputPerMtok: &output, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		format  types.RelayFormat
		mode    int
		request dto.Request
		want    int64
	}{
		{"embedding", types.RelayFormatEmbedding, relayconstant.RelayModeEmbeddings, &dto.EmbeddingRequest{Input: "fixture"}, 1000},
		{"gemini embedding", types.RelayFormatGemini, relayconstant.RelayModeGemini, &dto.GeminiEmbeddingRequest{}, 1000},
		{"gemini batch embedding", types.RelayFormatGemini, relayconstant.RelayModeGemini, &dto.GeminiBatchEmbeddingRequest{}, 1000},
		{"rerank", types.RelayFormatRerank, relayconstant.RelayModeRerank, &dto.RerankRequest{}, 1000},
		{"moderation", types.RelayFormatOpenAI, relayconstant.RelayModeModerations, &dto.GeneralOpenAIRequest{}, 1000},
		{"chat", types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, &dto.GeneralOpenAIRequest{}, 6000},
		{"responses", types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, &dto.OpenAIResponsesRequest{}, 6000},
		{"claude", types.RelayFormatClaude, relayconstant.RelayModeChatCompletions, &dto.ClaudeRequest{}, 6000},
		{"gemini chat", types.RelayFormatGemini, relayconstant.RelayModeGemini, &dto.GeminiChatRequest{}, 6000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{OriginModelName: "reserve-kind-test", UsingGroup: "direct", RelayFormat: tc.format, RelayMode: tc.mode, Request: tc.request}
			price, err := PriceQuoteHelper(c, info, 1000, tc.request.GetTokenCountMeta())
			if err != nil {
				t.Fatal(err)
			}
			// $1/M * 1000 input tokens = 1000 microUSD. Only generative
			// requests should reserve the additional $10/M * 500 output tokens.
			if int64(price.QuotaToPreConsume) != tc.want {
				t.Fatalf("reserved %d, want %d", price.QuotaToPreConsume, tc.want)
			}
		})
	}
}

func TestReservationQuotesOutputAtOutputPrice(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	defer func() { sqlDB, _ := db.DB(); _ = sqlDB.Close(); model.SetCoreDB(previousDB) }()
	if err := db.AutoMigrate(&model.ModelSellPrice{}); err != nil {
		t.Fatal(err)
	}
	input, output := 1.0, 10.0
	if err := db.Create(&model.ModelSellPrice{ModelName: "reserve-test", ChannelGroup: "direct", InputPerMtok: &input, OutputPerMtok: &output, Active: true, PricingVersion: 7}).Error; err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: "reserve-test", UsingGroup: "direct"}
	price, err := PriceQuoteHelper(c, info, 1000, &types.TokenCountMeta{MaxTokens: 2000, CompletionCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	// $1/M input * 1000 + $10/M output * 2000 * 2 = $0.041.
	if price.QuotaToPreConsume != 41000 || price.PricingVersion != 7 {
		t.Fatalf("output reserved at wrong price/version: %+v", price)
	}
}

func TestReservationDefaultsAndOverflow(t *testing.T) {
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}
	for _, meta := range []*types.TokenCountMeta{nil, {}, {CompletionCount: 3}, {MaxTokens: 80, CompletionCount: 2}} {
		usage := tokenReservationUsage(info, -10, meta)
		count, output := 1, common.PreConsumedQuota
		if meta != nil && meta.CompletionCount > 0 {
			count = meta.CompletionCount
		}
		if meta != nil && meta.MaxTokens > 0 {
			output = meta.MaxTokens
		}
		if usage.InputTokens < 0 || usage.OutputTokens != int64(output*count) {
			t.Fatalf("invalid default reservation: %+v", usage)
		}
	}
	usage := tokenReservationUsage(info, 100, &types.TokenCountMeta{MaxTokens: math.MaxInt, CompletionCount: 2})
	if usage.OutputTokens != math.MaxInt || usage.InputTokens <= 0 {
		t.Fatalf("overflow damaged reservation: %+v", usage)
	}
}

func TestSeedreamReservationCoversLayersAndRejectsMissingTiers(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	defer func() { sqlDB, _ := db.DB(); _ = sqlDB.Close(); model.SetCoreDB(previousDB) }()
	if err := db.AutoMigrate(&model.ModelSellPrice{}); err != nil {
		t.Fatal(err)
	}
	hi, layer, highLayer, ref := .09, .0225, .045, .003
	p := &billing.ImagePrices{OutputUSD: .045, PixelThreshold: 2610000, HighResolutionOutputUSD: &hi, LayerUSD: &layer, HighResolutionLayerUSD: &highLayer, ReferenceUSD: &ref, FreeReferences: 1}
	if err := db.Create(&model.ModelSellPrice{ModelName: "seedream-5.0-pro", ChannelGroup: "direct", RequestUSD: &p.OutputUSD, ImagePricing: p, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	request := &dto.ImageRequest{Model: "seedream-5.0-pro", LayerDecomposition: true, Image: []byte(`"https://example.org/ref.png"`)}
	if err := request.PrepareSeedream(); err != nil {
		t.Fatal(err)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{OriginModelName: request.Model, UsingGroup: "direct", Request: request}
	price, err := PriceQuoteHelper(c, info, 100, request.GetTokenCountMeta())
	if err != nil || price.QuotaToPreConsume != 765000 {
		t.Fatalf("under-reserved layers: %+v %v", price, err)
	}
	if err := db.Model(&model.ModelSellPrice{}).Where("model_name = ?", request.Model).Update("image_pricing", nil).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := PriceQuoteHelper(c, info, 100, request.GetTokenCountMeta()); err == nil {
		t.Fatal("accepted unpriced image usage")
	}
}
