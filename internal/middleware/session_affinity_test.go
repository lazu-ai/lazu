package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	relayrouter "github.com/lazu-ai/lazu/internal/relay/router"
)

const affinityTestModel = "affinity-test-model"

// countingAffinityStore records every read and write so a test can prove a
// request left the store alone.
type countingAffinityStore struct {
	mu     sync.Mutex
	inner  *relayrouter.MemoryAffinityStore
	gets   int
	sets   int
	failed bool
}

func newCountingAffinityStore() *countingAffinityStore {
	return &countingAffinityStore{inner: relayrouter.NewMemoryAffinityStore()}
}

func (s *countingAffinityStore) Get(ctx context.Context, key string, ttl time.Duration) (relayrouter.AffinityRecord, bool, error) {
	s.mu.Lock()
	s.gets++
	s.mu.Unlock()
	return s.inner.Get(ctx, key, ttl)
}

func (s *countingAffinityStore) Set(ctx context.Context, key string, record relayrouter.AffinityRecord, ttl time.Duration) error {
	s.mu.Lock()
	s.sets++
	s.mu.Unlock()
	return s.inner.Set(ctx, key, record, ttl)
}

func (s *countingAffinityStore) record(t *testing.T, key string) (relayrouter.AffinityRecord, bool) {
	t.Helper()
	record, ok, err := s.inner.Get(context.Background(), key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return record, ok
}

type affinityFixture struct {
	store   *countingAffinityStore
	user    *model.User
	token   *model.Token
	low     *model.Channel // what the session used before
	high    *model.Channel // what the router picks on its own
	engine  *gin.Engine
	hook    func(c *gin.Context)
	status  int
	request int
}

// newAffinityFixture seeds two channels for one model. The router always picks
// the higher priority one, so a request that lands on the lower one got there
// by session affinity.
func newAffinityFixture(t *testing.T, role int) *affinityFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Cleanup(setupDistributorTestDB(t))
	previousEnabled := common.SessionChannelAffinityEnabled
	common.SessionChannelAffinityEnabled = true
	t.Cleanup(func() { common.SessionChannelAffinityEnabled = previousEnabled })
	f := &affinityFixture{store: newCountingAffinityStore(), status: http.StatusOK}
	t.Cleanup(relayrouter.SetAffinityStoreForTest(f.store))

	f.user = &model.User{Id: 6101, Username: "affinity_user", Password: "password123", DisplayName: "affinity_user", Role: role, Status: common.UserStatusEnabled}
	if err := model.CoreDB().Create(f.user).Error; err != nil {
		t.Fatal(err)
	}
	f.token = &model.Token{Id: 6201, UserId: f.user.Id, Key: strings.Repeat("a", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100, Group: model.ChannelLaneDirect}
	if err := f.token.Insert(); err != nil {
		t.Fatal(err)
	}
	f.low = createAffinityTestChannel(t, 6301, 0, "low-key")
	f.high = createAffinityTestChannel(t, 6302, 100, "high-key")
	if err := (&model.Model{ModelName: affinityTestModel, Status: 1, LifecycleStatus: model.ModelLifecycleActive}).Insert(); err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()

	f.engine = gin.New()
	f.engine.Use(func(c *gin.Context) {
		f.request++
		c.Set(common.RequestIdKey, fmt.Sprintf("req-affinity-%s-%d", t.Name(), f.request))
		c.Next()
	})
	f.engine.POST("/v1/chat/completions", TokenAuth(), Distribute(), func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyProviderAttempted, true)
		if f.hook != nil {
			f.hook(c)
		}
		c.JSON(f.status, gin.H{
			"channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId),
			"key":        common.GetContextKeyString(c, constant.ContextKeyChannelKey),
			"affinity":   common.GetContextKeyString(c, constant.ContextKeySessionAffinityResult),
			"reason":     common.GetContextKeyString(c, constant.ContextKeySessionAffinityReason),
		})
	})
	return f
}

func createAffinityTestChannel(t *testing.T, id int, priority int64, key string) *model.Channel {
	t.Helper()
	channel := &model.Channel{Id: id, Type: constant.ChannelTypeOpenAI, Key: key, Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("affinity-%d", id), Models: affinityTestModel, Group: model.ChannelLaneDirect, Priority: common.GetPointer(priority)}
	if err := model.CoreDB().Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneDirect, Model: affinityTestModel, ChannelId: id, Enabled: true, Priority: common.GetPointer(priority)}).Error; err != nil {
		t.Fatal(err)
	}
	return channel
}

type affinityResponse struct {
	ChannelID int    `json:"channel_id"`
	Key       string `json:"key"`
	Affinity  string `json:"affinity"`
	Reason    string `json:"reason"`
	requestID string
}

// send makes one turn of a Claude Code session. An empty session sends no
// session id, which leaves the request on the per-Key fallback.
func (f *affinityFixture) send(t *testing.T, session string, bearer string) affinityResponse {
	t.Helper()
	body, _ := json.Marshal(gin.H{"model": affinityTestModel, "messages": []gin.H{{"role": "user", "content": "hello"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer == "" {
		bearer = f.token.Key
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if session != "" {
		req.Header.Set("X-Claude-Code-Session-Id", session)
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	if w.Code != f.status {
		t.Fatalf("status %d, want %d: %s", w.Code, f.status, w.Body.String())
	}
	var response affinityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	response.requestID = fmt.Sprintf("req-affinity-%s-%d", t.Name(), f.request)
	return response
}

// plainKey is the key a fixture channel was created with; the stored copy is
// encrypted at rest.
func plainKey(channel *model.Channel) string {
	return map[int]string{6301: "low-key", 6302: "high-key"}[channel.Id]
}

func (f *affinityFixture) key(session string) string {
	return relayrouter.SessionAffinityKey(f.user.Id, session, affinityTestModel)
}

func (f *affinityFixture) seed(t *testing.T, session string, channel *model.Channel) {
	t.Helper()
	record := relayrouter.AffinityRecord{ChannelID: channel.Id, Group: model.ChannelLaneDirect, KeyHash: relayrouter.AffinityKeyHash(plainKey(channel))}
	if err := f.store.inner.Set(context.Background(), f.key(session), record, time.Hour); err != nil {
		t.Fatal(err)
	}
}

func (f *affinityFixture) wantRecord(t *testing.T, session string, channel *model.Channel) {
	t.Helper()
	record, ok := f.store.record(t, f.key(session))
	if !ok || record.ChannelID != channel.Id || record.Group != model.ChannelLaneDirect || record.KeyHash != relayrouter.AffinityKeyHash(plainKey(channel)) {
		t.Fatalf("record = %+v (found %v), want channel %d", record, ok, channel.Id)
	}
}

func decisionFor(t *testing.T, requestID string) model.RoutingDecision {
	t.Helper()
	var decision model.RoutingDecision
	if err := model.CoreDB().Where("request_id = ?", requestID).First(&decision).Error; err != nil {
		t.Fatalf("routing decision for %s: %v", requestID, err)
	}
	return decision
}

func TestSessionAffinityFirstTurnRecordsRoutedChannel(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	got := f.send(t, "sess-first", "")
	if got.ChannelID != f.high.Id || got.Affinity != sessionAffinityNone {
		t.Fatalf("first turn: %+v", got)
	}
	f.wantRecord(t, "sess-first", f.high)
	if decision := decisionFor(t, got.requestID); decision.DecisionReason == relayrouter.DecisionReasonSessionAffinity {
		t.Fatalf("a first turn is not an affinity decision: %+v", decision)
	}
}

func TestSessionAffinityNextTurnReusesRecordedChannel(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	f.seed(t, "sess-next", f.low)
	for turn := 0; turn < 3; turn++ {
		got := f.send(t, "sess-next", "")
		if got.ChannelID != f.low.Id || got.Affinity != sessionAffinityHit {
			t.Fatalf("turn %d left the session's channel: %+v", turn, got)
		}
		decision := decisionFor(t, got.requestID)
		if decision.DecisionReason != relayrouter.DecisionReasonSessionAffinity || decision.StrategyName != relayrouter.DecisionReasonSessionAffinity ||
			decision.ChoseChannel == nil || *decision.ChoseChannel != f.low.Id {
			t.Fatalf("decision does not say affinity chose channel %d: %+v", f.low.Id, decision)
		}
	}
	f.wantRecord(t, "sess-next", f.low)
	// A hit on an unchanged record is renewed by the lookup and never rewritten.
	if f.store.sets != 0 {
		t.Fatalf("unchanged record was rewritten %d times", f.store.sets)
	}
	// Another session of the same user is routed on its own.
	if got := f.send(t, "sess-other", ""); got.ChannelID != f.high.Id {
		t.Fatalf("another session inherited the record: %+v", got)
	}
}

func TestSessionAffinityFallsBackAndRewritesWhenChannelIsDisabled(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	f.seed(t, "sess-disabled", f.low)
	if err := model.CoreDB().Model(&model.Channel{}).Where("id = ?", f.low.Id).Update("status", common.ChannelStatusAutoDisabled).Error; err != nil {
		t.Fatal(err)
	}
	got := f.send(t, "sess-disabled", "")
	if got.ChannelID != f.high.Id || got.Affinity != sessionAffinityRejected || got.Reason != relayrouter.AffinitySkipChannelUnavailable {
		t.Fatalf("disabled channel: %+v", got)
	}
	f.wantRecord(t, "sess-disabled", f.high)
	// The session now stays on the channel it moved to, even after the old
	// one comes back.
	if err := model.CoreDB().Model(&model.Channel{}).Where("id = ?", f.low.Id).Update("status", common.ChannelStatusEnabled).Error; err != nil {
		t.Fatal(err)
	}
	if got := f.send(t, "sess-disabled", ""); got.ChannelID != f.high.Id || got.Affinity != sessionAffinityHit {
		t.Fatalf("session did not stay on its new channel: %+v", got)
	}
}

func TestSessionAffinityFollowsRetryToAnotherChannel(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	f.seed(t, "sess-retry", f.low)
	// The relay controller re-runs channel setup when a retry switches
	// channel; the handler does the same and then succeeds.
	f.hook = func(c *gin.Context) {
		retryChannel, err := model.CacheGetChannel(f.high.Id)
		if err != nil {
			t.Fatal(err)
		}
		if lazuErr := SetupContextForSelectedChannel(c, retryChannel, affinityTestModel); lazuErr != nil {
			t.Fatal(lazuErr)
		}
	}
	got := f.send(t, "sess-retry", "")
	if got.ChannelID != f.high.Id || got.Affinity != sessionAffinityHit {
		t.Fatalf("retry: %+v", got)
	}
	f.wantRecord(t, "sess-retry", f.high)
	f.hook = nil
	if got := f.send(t, "sess-retry", ""); got.ChannelID != f.high.Id || got.Affinity != sessionAffinityHit {
		t.Fatalf("next turn did not follow the retry: %+v", got)
	}
}

func TestSessionAffinityKeepsRecordWhenTurnFails(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	f.status = http.StatusBadGateway
	got := f.send(t, "sess-failed", "")
	if got.ChannelID != f.high.Id {
		t.Fatalf("failed turn: %+v", got)
	}
	if _, ok := f.store.record(t, f.key("sess-failed")); ok || f.store.sets != 0 {
		t.Fatalf("a failed turn wrote a record (sets=%d)", f.store.sets)
	}
}

func TestSessionAffinityTokenPinnedChannelWins(t *testing.T) {
	f := newAffinityFixture(t, common.RoleRootUser)
	f.seed(t, "sess-pinned", f.low)
	got := f.send(t, "sess-pinned", fmt.Sprintf("%s-%d", f.token.Key, f.high.Id))
	if got.ChannelID != f.high.Id || got.Affinity != "" {
		t.Fatalf("pinned Key: %+v", got)
	}
	if f.store.gets != 0 || f.store.sets != 0 {
		t.Fatalf("a Key pinned to a channel touched the affinity store: gets=%d sets=%d", f.store.gets, f.store.sets)
	}
	if decision := decisionFor(t, got.requestID); decision.StrategyName != "token_specific_channel" {
		t.Fatalf("pinned decision: %+v", decision)
	}
	f.wantRecord(t, "sess-pinned", f.low)
}

func TestSessionAffinitySiteSwitchOff(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	f.seed(t, "sess-off", f.low)
	common.SessionChannelAffinityEnabled = false
	got := f.send(t, "sess-off", "")
	if got.ChannelID != f.high.Id || got.Affinity != "" {
		t.Fatalf("switch off: %+v", got)
	}
	if f.store.gets != 0 || f.store.sets != 0 {
		t.Fatalf("switch off still touched the store: gets=%d sets=%d", f.store.gets, f.store.sets)
	}
}

func TestSessionAffinityIgnoresPerKeyFallbackSession(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	got := f.send(t, "", "")
	if got.Affinity != "" || f.store.gets != 0 || f.store.sets != 0 {
		t.Fatalf("a request without an agent session took part: %+v gets=%d sets=%d", got, f.store.gets, f.store.sets)
	}
}

func TestSessionAffinityPinsUpstreamKey(t *testing.T) {
	f := newAffinityFixture(t, common.RoleCommonUser)
	keys := []string{"multi-key-0", "multi-key-1", "multi-key-2"}
	if err := model.CoreDB().Model(&model.Channel{}).Where("id = ?", f.low.Id).Updates(map[string]any{
		"key":          strings.Join(keys, "\n"),
		"channel_info": model.ChannelInfo{IsMultiKey: true, MultiKeySize: len(keys), MultiKeyMode: constant.MultiKeyModePolling},
	}).Error; err != nil {
		t.Fatal(err)
	}
	record := relayrouter.AffinityRecord{ChannelID: f.low.Id, Group: model.ChannelLaneDirect, KeyIndex: 2, KeyHash: relayrouter.AffinityKeyHash(keys[2])}
	if err := f.store.inner.Set(context.Background(), f.key("sess-key"), record, time.Hour); err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 3; turn++ {
		if got := f.send(t, "sess-key", ""); got.ChannelID != f.low.Id || got.Key != keys[2] {
			t.Fatalf("turn %d: %+v, want key %q", turn, got, keys[2])
		}
	}
	// A key list that changed under the index is not the recorded key: the
	// channel rotates, and the record follows whatever key served the turn.
	record.KeyHash = relayrouter.AffinityKeyHash("a key that is gone")
	if err := f.store.inner.Set(context.Background(), f.key("sess-key"), record, time.Hour); err != nil {
		t.Fatal(err)
	}
	got := f.send(t, "sess-key", "")
	if got.ChannelID != f.low.Id {
		t.Fatalf("stale key: %+v", got)
	}
	stored, _ := f.store.record(t, f.key("sess-key"))
	if stored.KeyHash != relayrouter.AffinityKeyHash(got.Key) || keys[stored.KeyIndex] != got.Key {
		t.Fatalf("record %+v does not name the key that served (%q)", stored, got.Key)
	}
}
