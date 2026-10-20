package router

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"gorm.io/gorm"
)

func setupRouterTestDB(t *testing.T) func() {
	t.Helper()
	previousDB := model.CoreDB()
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	db, err := gorm.Open(gormsqlite.Open("file:router_priority?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
		t.Fatalf("failed to migrate router tables: %v", err)
	}
	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
	}
}

func TestProtocolRoutingPrefersNativeOverHigherPriorityBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupRouterTestDB(t)
	defer cleanup()

	highPriorityBridge := &model.Channel{Id: 9301, Type: constant.ChannelTypeOpenAI, Key: "bridge", Status: common.ChannelStatusEnabled, Name: "chat-only", Models: "test-model", Group: "direct", Priority: common.GetPointer[int64](100), OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`}
	lowerPriorityNative := &model.Channel{Id: 9302, Type: constant.ChannelTypeOpenAI, Key: "native", Status: common.ChannelStatusEnabled, Name: "responses-native", Models: "test-model", Group: "direct", Priority: common.GetPointer[int64](1), OtherSettings: `{"protocol_dialect":"openai"}`}
	for _, channel := range []*model.Channel{highPriorityBridge, lowerPriorityNative} {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		if err := model.CoreDB().Create(&model.Ability{Group: channel.Group, Model: "test-model", ChannelId: channel.Id, Enabled: true}).Error; err != nil {
			t.Fatalf("create ability: %v", err)
		}
	}

	c, _ := gin.CreateTestContext(nil)
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "test-model"}, Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}}}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: "openai"}, Semantic: request})
	channel, _, err := SelectProtocolCompatibleChannel(c, "direct", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if channel == nil || channel.Id != lowerPriorityNative.Id {
		t.Fatalf("selected channel=%v, want native channel %d", channel, lowerPriorityNative.Id)
	}
}

func TestProtocolRoutingSkipsChannelBelowEstimatedInputContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupRouterTestDB(t)
	defer cleanup()

	channels := []*model.Channel{
		{Id: 9401, Type: constant.ChannelTypeOpenAI, Key: "small", Status: common.ChannelStatusEnabled, Name: "small-context", Models: "test-model", Group: "direct", OtherSettings: `{"protocol_dialect":"openai","capabilities":{"context_window":4096}}`},
		{Id: 9402, Type: constant.ChannelTypeOpenAI, Key: "large", Status: common.ChannelStatusEnabled, Name: "large-context", Models: "test-model", Group: "direct", OtherSettings: `{"protocol_dialect":"openai","capabilities":{"context_window":32768}}`},
	}
	for _, channel := range channels {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		if err := model.CoreDB().Create(&model.Ability{Group: channel.Group, Model: "test-model", ChannelId: channel.Id, Enabled: true}).Error; err != nil {
			t.Fatalf("create ability: %v", err)
		}
	}

	c, _ := gin.CreateTestContext(nil)
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "test-model"}, Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "long input"}}}}}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: "openai"}, Semantic: request, InputTokens: 8192})
	decision, err := (&PriorityRouter{}).Select(context.Background(), &RequestContext{
		GinContext:      c,
		ResolvedModel:   "test-model",
		CandidateGroups: []string{"direct"},
		DefaultGroup:    "direct",
		Features:        &Features{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision == nil || decision.ChannelID != 9402 {
		t.Fatalf("selected channel=%v, want larger context channel 9402", decision)
	}
	parsed, ok := common.GetContextKeyType[*textgen.ParsedRequest](c, constant.ContextKeyTextgenParsed)
	if !ok || parsed == nil || parsed.InputTokens != 8192 {
		t.Fatalf("input token estimate was not retained for retries: %+v", parsed)
	}
}

func TestPriorityRouterSelectsExistingChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupRouterTestDB(t)
	defer cleanup()

	channel := &model.Channel{
		Id:       9101,
		Type:     constant.ChannelTypeOpenAI,
		Key:      "upstream-key",
		Status:   common.ChannelStatusEnabled,
		Name:     "baseline-channel",
		Models:   "gpt-test",
		Group:    "direct",
		Priority: common.GetPointer[int64](10),
	}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("failed to create channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group:     "direct",
		Model:     "gpt-test",
		ChannelId: channel.Id,
		Enabled:   true,
	}).Error; err != nil {
		t.Fatalf("failed to create ability: %v", err)
	}

	r := &PriorityRouter{}
	decision, err := r.Select(context.Background(), &RequestContext{
		ResolvedModel:   "gpt-test",
		CandidateGroups: []string{"direct"},
		DefaultGroup:    "direct",
	})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if decision.ChannelID != channel.Id {
		t.Fatalf("channel=%d want=%d", decision.ChannelID, channel.Id)
	}
	if decision.Group != "direct" {
		t.Fatalf("group=%q", decision.Group)
	}
}

func TestPriorityRouterPrefersTaggedChannelForMultiImageOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupRouterTestDB(t)
	defer cleanup()

	cloudflare := &model.Channel{
		Id:            9501,
		Type:          constant.ChannelTypeOpenAI,
		Key:           "cloudflare-key",
		Status:        common.ChannelStatusEnabled,
		Name:          "cloudflare",
		Models:        "kimi-k2.6",
		Group:         "direct",
		Priority:      common.GetPointer[int64](100),
		OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`,
	}
	marketplace := &model.Channel{
		Id:            9502,
		Type:          constant.ChannelTypeOpenAI,
		Key:           "marketplace-key",
		Status:        common.ChannelStatusEnabled,
		Name:          "marketplace",
		Models:        "kimi-k2.6",
		Group:         "direct",
		Priority:      common.GetPointer[int64](10),
		OtherSettings: `{"protocol_dialect":"openai_chat_compatible","capabilities":{"preferred_for":["multi_image"]}}`,
	}
	for _, channel := range []*model.Channel{cloudflare, marketplace} {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		if err := model.CoreDB().Create(&model.Ability{
			Group: channel.Group, Model: "kimi-k2.6", ChannelId: channel.Id,
			Enabled: true, Priority: channel.Priority, Weight: uint(channel.GetWeight()),
		}).Error; err != nil {
			t.Fatalf("create ability: %v", err)
		}
	}

	c, _ := gin.CreateTestContext(nil)
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "kimi-k2.6"}, Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "inspect these images"}}}}}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai"}, Semantic: request})
	router := &PriorityRouter{}

	for _, test := range []struct {
		name       string
		imageCount int
		wantID     int
		wantReason string
	}{
		{name: "text", imageCount: 0, wantID: cloudflare.Id, wantReason: "priority_weight"},
		{name: "single image", imageCount: 1, wantID: cloudflare.Id, wantReason: "priority_weight"},
		{name: "multiple images", imageCount: 2, wantID: marketplace.Id, wantReason: "capability_preference"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := router.Select(context.Background(), &RequestContext{
				GinContext: c, ResolvedModel: "kimi-k2.6", CandidateGroups: []string{"direct"},
				DefaultGroup: "direct", Features: &Features{HasVision: test.imageCount > 0, VisionImageCount: test.imageCount},
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.ChannelID != test.wantID || decision.Reason != test.wantReason {
				t.Fatalf("decision=(channel=%d reason=%q), want=(channel=%d reason=%q)", decision.ChannelID, decision.Reason, test.wantID, test.wantReason)
			}
		})
	}

	channel, _, err := selectProtocolCompatibleChannel(
		c, "direct", "kimi-k2.6", map[int]struct{}{marketplace.Id: {}}, []string{routingIntentMultiImage},
	)
	if err != nil {
		t.Fatal(err)
	}
	if channel == nil || channel.Id != cloudflare.Id {
		t.Fatalf("fallback channel=%v, want cloudflare %d", channel, cloudflare.Id)
	}
}

func TestProtocolRoutingRetryPreservesMultiImagePreference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupRouterTestDB(t)
	defer cleanup()

	channels := []*model.Channel{
		{Id: 9601, Type: constant.ChannelTypeOpenAI, Key: "preferred-1", Status: common.ChannelStatusEnabled, Name: "preferred-1", Models: "kimi-k2.6", Group: "direct", Priority: common.GetPointer[int64](10), OtherSettings: `{"protocol_dialect":"openai_chat_compatible","capabilities":{"preferred_for":["multi_image"]}}`},
		{Id: 9602, Type: constant.ChannelTypeOpenAI, Key: "preferred-2", Status: common.ChannelStatusEnabled, Name: "preferred-2", Models: "kimi-k2.6", Group: "direct", Priority: common.GetPointer[int64](5), OtherSettings: `{"protocol_dialect":"openai_chat_compatible","capabilities":{"preferred_for":["multi_image"]}}`},
		{Id: 9603, Type: constant.ChannelTypeOpenAI, Key: "default", Status: common.ChannelStatusEnabled, Name: "default", Models: "kimi-k2.6", Group: "direct", Priority: common.GetPointer[int64](100), OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`},
	}
	for _, channel := range channels {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		if err := model.CoreDB().Create(&model.Ability{
			Group: channel.Group, Model: "kimi-k2.6", ChannelId: channel.Id,
			Enabled: true, Priority: channel.Priority, Weight: uint(channel.GetWeight()),
		}).Error; err != nil {
			t.Fatalf("create ability: %v", err)
		}
	}

	c, _ := gin.CreateTestContext(nil)
	request := &textgen.Request{
		Model: textgen.ModelRef{Canonical: "kimi-k2.6"},
		Conversation: []textgen.InputItem{{
			Kind: textgen.InputMessage, Role: "user",
			Content: []textgen.ContentPart{
				{Kind: textgen.ContentText, Text: "compare"},
				{Kind: textgen.ContentImage, URL: "https://example.test/1.png"},
				{Kind: textgen.ContentImage, URL: "https://example.test/2.png"},
			},
		}},
	}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{
		Client:   textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai"},
		Semantic: request,
	})

	channel, _, err := SelectProtocolCompatibleChannel(
		c, "direct", "kimi-k2.6", map[int]struct{}{channels[0].Id: {}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if channel == nil || channel.Id != channels[1].Id {
		t.Fatalf("retry channel=%v, want remaining multi-image preferred channel %d", channel, channels[1].Id)
	}
}

// A candidate that can serve the request only through a registered
// degradation is lossy: a lower-priority candidate that carries it
// losslessly wins, and the degraded one is still taken when it is all there is.
func TestProtocolRoutingRanksDegradedPlansBelowLosslessOnes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupRouterTestDB(t)
	defer cleanup()

	degraded := &model.Channel{Id: 9311, Type: constant.ChannelTypeOpenAI, Key: "degraded", Status: common.ChannelStatusEnabled, Name: "no-schema", Models: "test-model", Group: "direct", Priority: common.GetPointer[int64](100), OtherSettings: `{"protocol_dialect":"openai_chat_compatible","capabilities":{"supports_json_schema":false}}`}
	lossless := &model.Channel{Id: 9312, Type: constant.ChannelTypeOpenAI, Key: "lossless", Status: common.ChannelStatusEnabled, Name: "schema", Models: "test-model", Group: "direct", Priority: common.GetPointer[int64](1), OtherSettings: `{"protocol_dialect":"openai_chat_compatible"}`}
	for _, channel := range []*model.Channel{degraded, lossless} {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		if err := model.CoreDB().Create(&model.Ability{Group: channel.Group, Model: "test-model", ChannelId: channel.Id, Enabled: true}).Error; err != nil {
			t.Fatalf("create ability: %v", err)
		}
	}
	c, _ := gin.CreateTestContext(nil)
	request := &textgen.Request{
		Model:        textgen.ModelRef{Canonical: "test-model"},
		Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}},
		Output:       textgen.OutputConstraint{Type: "json_schema", Name: "t", Schema: []byte(`{"type":"object"}`)},
	}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai"}, Semantic: request})
	channel, _, err := SelectProtocolCompatibleChannel(c, "direct", "test-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if channel == nil || channel.Id != lossless.Id {
		t.Fatalf("selected channel=%v, want lossless channel %d", channel, lossless.Id)
	}
	channel, _, err = SelectProtocolCompatibleChannel(c, "direct", "test-model", map[int]struct{}{lossless.Id: {}})
	if err != nil {
		t.Fatal(err)
	}
	if channel == nil || channel.Id != degraded.Id {
		t.Fatalf("selected channel=%v, want the degraded channel %d when it is the only one", channel, degraded.Id)
	}
}
