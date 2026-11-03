package router

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestSessionAffinityKeyScopesUserSessionAndModel(t *testing.T) {
	base := SessionAffinityKey(7, "sess-1", "model-a")
	if base != SessionAffinityKey(7, "sess-1", "model-a") {
		t.Fatal("key is not stable")
	}
	for name, other := range map[string]string{
		"user":    SessionAffinityKey(8, "sess-1", "model-a"),
		"session": SessionAffinityKey(7, "sess-2", "model-a"),
		"model":   SessionAffinityKey(7, "sess-1", "model-b"),
	} {
		if other == base {
			t.Fatalf("a different %s shares the key %s", name, base)
		}
	}
}

func TestMemoryAffinityStoreRenewsOnHitAndExpires(t *testing.T) {
	store := NewMemoryAffinityStore()
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	store.SetClockForTest(func() time.Time { return now })
	ctx := context.Background()
	record := AffinityRecord{ChannelID: 3, Group: "direct", KeyIndex: 1, KeyHash: AffinityKeyHash("k")}
	if err := store.Set(ctx, "s", record, time.Hour); err != nil {
		t.Fatal(err)
	}

	now = now.Add(50 * time.Minute)
	if got, ok, _ := store.Get(ctx, "s", time.Hour); !ok || got != record {
		t.Fatalf("record lost before its TTL: %+v %v", got, ok)
	}
	// The hit renewed the hour: 50 more minutes is still inside it.
	now = now.Add(50 * time.Minute)
	if _, ok, _ := store.Get(ctx, "s", time.Hour); !ok {
		t.Fatal("hit did not renew the TTL")
	}
	now = now.Add(61 * time.Minute)
	if _, ok, _ := store.Get(ctx, "s", time.Hour); ok {
		t.Fatal("record outlived its TTL")
	}
}

func TestRedisAffinityStoreRoundTrip(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opt)
	defer client.Close()
	ctx := context.Background()
	store := redisAffinityStore{client: client}
	key := sessionAffinityKeyPrefix + "test:" + t.Name()
	defer client.Del(ctx, key)

	if _, ok, err := store.Get(ctx, key, time.Hour); err != nil || ok {
		t.Fatalf("missing key: ok=%v err=%v", ok, err)
	}
	record := AffinityRecord{ChannelID: 11, Group: "direct", KeyIndex: 2, KeyHash: "abcd"}
	if err := store.Set(ctx, key, record, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(ctx, key, time.Hour)
	if err != nil || !ok || got != record {
		t.Fatalf("round trip: got=%+v ok=%v err=%v", got, ok, err)
	}
	if ttl := client.TTL(ctx, key).Val(); ttl <= time.Minute {
		t.Fatalf("Get did not renew the TTL to an hour: %s", ttl)
	}
}

func createAffinityChannel(t *testing.T, id int, group string, priority int64, status int) *model.Channel {
	t.Helper()
	channel := &model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: "key-" + group, Status: status, Name: "affinity", Models: "test-model", Group: group, Priority: common.GetPointer(priority), OtherSettings: `{"protocol_dialect":"openai"}`}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{Group: group, Model: "test-model", ChannelId: id, Enabled: true, Priority: common.GetPointer(priority)}).Error; err != nil {
		t.Fatalf("create ability: %v", err)
	}
	return channel
}

func affinityRequest(groups ...string) *RequestContext {
	c, _ := gin.CreateTestContext(nil)
	request := &textgen.Request{Model: textgen.ModelRef{Canonical: "test-model"}, Conversation: []textgen.InputItem{{Kind: textgen.InputMessage, Role: "user", Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}}}}}
	common.SetContextKey(c, constant.ContextKeyTextgenParsed, &textgen.ParsedRequest{Client: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses, Dialect: "openai"}, Semantic: request})
	return &RequestContext{GinContext: c, ResolvedModel: "test-model", RequestedModel: "test-model", CandidateGroups: groups, DefaultGroup: groups[0]}
}

func TestSelectByAffinityPrefersRecordedChannelOverPriority(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer setupRouterTestDB(t)()
	low := createAffinityChannel(t, 9601, "direct", 0, common.ChannelStatusEnabled)
	createAffinityChannel(t, 9602, "direct", 100, common.ChannelStatusEnabled)

	decision, reason := SelectByAffinity(affinityRequest("direct"), AffinityRecord{ChannelID: low.Id, Group: "direct"})
	if decision == nil {
		t.Fatalf("affinity rejected: %s", reason)
	}
	if decision.ChannelID != low.Id || decision.Reason != DecisionReasonSessionAffinity || decision.Meta["strategy_name"] != DecisionReasonSessionAffinity {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	// The router alone always takes the higher priority channel.
	routed, err := (&PriorityRouter{}).Select(context.Background(), affinityRequest("direct"))
	if err != nil || routed.ChannelID == low.Id {
		t.Fatalf("router should prefer the higher priority channel: %+v %v", routed, err)
	}
}

func TestSelectByAffinityRejectsUnavailableChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer setupRouterTestDB(t)()
	disabled := createAffinityChannel(t, 9611, "direct", 0, common.ChannelStatusManuallyDisabled)
	createAffinityChannel(t, 9612, "direct", 0, common.ChannelStatusEnabled)

	if decision, reason := SelectByAffinity(affinityRequest("direct"), AffinityRecord{ChannelID: disabled.Id, Group: "direct"}); decision != nil || reason != AffinitySkipChannelUnavailable {
		t.Fatalf("disabled channel: decision=%+v reason=%s", decision, reason)
	}
	// A channel that no longer serves the model is the same case.
	if decision, reason := SelectByAffinity(affinityRequest("direct"), AffinityRecord{ChannelID: 424242, Group: "direct"}); decision != nil || reason != AffinitySkipChannelUnavailable {
		t.Fatalf("unknown channel: decision=%+v reason=%s", decision, reason)
	}
}

func TestSelectByAffinityRespectsLanes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer setupRouterTestDB(t)()
	direct := createAffinityChannel(t, 9621, "direct", 0, common.ChannelStatusEnabled)

	if decision, reason := SelectByAffinity(affinityRequest("agent"), AffinityRecord{ChannelID: direct.Id, Group: "direct"}); decision != nil || reason != AffinitySkipLaneChanged {
		t.Fatalf("lane the request may not use: decision=%+v reason=%s", decision, reason)
	}
	// Recorded on the fallback lane while the preferred lane had nothing.
	if decision, reason := SelectByAffinity(affinityRequest("premium", "direct"), AffinityRecord{ChannelID: direct.Id, Group: "direct"}); decision == nil {
		t.Fatalf("fallback lane with an empty preferred lane: %s", reason)
	}
	// Once the preferred lane has a candidate again, the session returns to it.
	createAffinityChannel(t, 9622, "premium", 0, common.ChannelStatusEnabled)
	if decision, reason := SelectByAffinity(affinityRequest("premium", "direct"), AffinityRecord{ChannelID: direct.Id, Group: "direct"}); decision != nil || reason != AffinitySkipPreferredLane {
		t.Fatalf("preferred lane available: decision=%+v reason=%s", decision, reason)
	}
}

func TestSelectByAffinityRejectsRetreatingRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defer setupRouterTestDB(t)()
	if err := model.CoreDB().AutoMigrate(&model.RouteHealthState{}); err != nil {
		t.Fatal(err)
	}
	channel := createAffinityChannel(t, 9631, "direct", 0, common.ChannelStatusEnabled)
	until := time.Now().UTC().Add(time.Hour)
	if err := model.CoreDB().Create(&model.RouteHealthState{Lane: "direct", ModelName: "test-model", ChannelId: channel.Id, State: model.RouteHealthStateRetreat, ConsecutiveFailures: 5, RetreatUntil: &until, LastEvidenceAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	model.RefreshRouteHealthCache()
	defer func() {
		model.CoreDB().Where("channel_id = ?", channel.Id).Delete(&model.RouteHealthState{})
		model.RefreshRouteHealthCache()
	}()

	if decision, reason := SelectByAffinity(affinityRequest("direct"), AffinityRecord{ChannelID: channel.Id, Group: "direct"}); decision != nil || reason != AffinitySkipRetreating {
		t.Fatalf("retreating route: decision=%+v reason=%s", decision, reason)
	}
}
