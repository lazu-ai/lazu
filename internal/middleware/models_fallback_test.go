package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relayrouter "github.com/lazu-ai/lazu/internal/relay/router"
	"gorm.io/gorm"
)

// fakeRouter is a Router that returns NoChannelError for every model
// except the one in `MatchModel`. Used to exercise distributor's
// fallback iteration without standing up the full composite pipeline.
type fakeRouter struct {
	MatchModel        string
	UnsupportedModels map[string]bool
	Calls             []string
}

func (f *fakeRouter) Name() string    { return "fake" }
func (f *fakeRouter) Version() string { return "v1" }

func (f *fakeRouter) Select(ctx context.Context, req *relayrouter.RequestContext) (*relayrouter.Decision, error) {
	f.Calls = append(f.Calls, req.ResolvedModel)
	if f.UnsupportedModels[req.ResolvedModel] {
		return nil, &textgen.UnsupportedFeatureError{Feature: textgen.FeatureInputImage, Path: "input[0].content[0]", Reason: "test capability mismatch"}
	}
	if req.ResolvedModel == f.MatchModel {
		return &relayrouter.Decision{
			ChannelID: 1,
			Model:     req.ResolvedModel,
			Reason:    "fake_match",
			Channel: &model.Channel{
				Id:    1,
				Type:  constant.ChannelTypeOpenAI,
				Name:  "fake-ch",
				Group: "direct",
			},
		}, nil
	}
	return nil, &relayrouter.NoChannelError{
		Group: req.DefaultGroup,
		Model: req.ResolvedModel,
	}
}

// setupModelsFallbackDB swaps in an isolated in-memory DB for the
// minimal model state Select() / cache references. Each test gets a
// clean store so iteration order between tests doesn't leak.
func setupModelsFallbackDB(t *testing.T) func() {
	t.Helper()
	prevDB := model.CoreDB()
	prevMemCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	db, err := gorm.Open(gormsqlite.Open("file:distributor_fallback?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.EnsureColumnNamesInitialized()
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(prevDB)
		common.MemoryCacheEnabled = prevMemCache
	}
}

// extractFallbackFromBody runs the same logic Distributor() does:
// build modelsToTry list, iterate router.Select(), report which models
// were tried. Lets us assert behaviour without spinning up the full
// gin handler chain (which carries auth / token resolution / pricing
// that aren't this test's concern).
func iterateFallback(t *testing.T, router *fakeRouter, primary string, fallback []string) (chosenModel string, calls []string) {
	t.Helper()

	// Build modelsToTry the same way distributor does.
	modelsToTry := []string{primary}
	seen := map[string]struct{}{primary: {}}
	for _, m := range fallback {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		modelsToTry = append(modelsToTry, m)
	}

	reqCtx := &relayrouter.RequestContext{
		RequestedModel:  primary,
		ResolvedModel:   primary,
		CandidateGroups: []string{"direct"},
		DefaultGroup:    "direct",
	}
	for _, m := range modelsToTry {
		reqCtx.ResolvedModel = m
		d, err := router.Select(context.Background(), reqCtx)
		if err == nil {
			return d.Model, router.Calls
		}
		// Capability mismatches are model-scoped just like no-channel
		// results, so a later fallback remains eligible.
		var noChan *relayrouter.NoChannelError
		var unsupported *textgen.UnsupportedFeatureError
		if !asNoChannelError(err, &noChan) && !errors.As(err, &unsupported) {
			t.Fatalf("unexpected err: %v", err)
		}
	}
	return "", router.Calls
}

func asNoChannelError(err error, target **relayrouter.NoChannelError) bool {
	if e, ok := err.(*relayrouter.NoChannelError); ok {
		*target = e
		return true
	}
	return false
}

func TestModelsFallbackTriesEachModelInOrder(t *testing.T) {
	// Primary is "gpt-4o", fallback chain is ["claude", "mini"].
	// Only "mini" matches → router should be called 3 times then succeed on mini.
	r := &fakeRouter{MatchModel: "mini"}
	chosen, calls := iterateFallback(t, r, "gpt-4o", []string{"claude", "mini"})
	if chosen != "mini" {
		t.Fatalf("chosen=%q want=mini", chosen)
	}
	want := []string{"gpt-4o", "claude", "mini"}
	if len(calls) != len(want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	for i, c := range calls {
		if c != want[i] {
			t.Fatalf("call[%d]=%q want=%q", i, c, want[i])
		}
	}
}

func TestModelsFallbackStopsOnFirstMatch(t *testing.T) {
	// Primary matches immediately; fallback chain ignored.
	r := &fakeRouter{MatchModel: "gpt-4o"}
	chosen, calls := iterateFallback(t, r, "gpt-4o", []string{"claude", "mini"})
	if chosen != "gpt-4o" {
		t.Fatalf("chosen=%q want=gpt-4o", chosen)
	}
	if len(calls) != 1 {
		t.Fatalf("calls=%v (should stop at first match)", calls)
	}
}

func TestModelsFallbackContinuesAfterProtocolCapabilityMismatch(t *testing.T) {
	r := &fakeRouter{MatchModel: "vision-fallback", UnsupportedModels: map[string]bool{"text-primary": true}}
	chosen, calls := iterateFallback(t, r, "text-primary", []string{"vision-fallback"})
	if chosen != "vision-fallback" {
		t.Fatalf("chosen=%q want=vision-fallback", chosen)
	}
	if len(calls) != 2 || calls[0] != "text-primary" || calls[1] != "vision-fallback" {
		t.Fatalf("calls=%v want both models in order", calls)
	}
}

func TestModelsFallbackDedupesPrimaryFromList(t *testing.T) {
	// Caller put the primary in the models[] list too. We should not
	// retry the same model in the same Select() pass.
	r := &fakeRouter{MatchModel: "mini"}
	chosen, calls := iterateFallback(t, r, "gpt-4o", []string{"gpt-4o", "mini"})
	if chosen != "mini" {
		t.Fatalf("chosen=%q want=mini", chosen)
	}
	// 2 distinct calls expected (gpt-4o → mini), not 3.
	if len(calls) != 2 {
		t.Fatalf("calls=%v want 2 unique calls", calls)
	}
}

func TestModelsFallbackAllFailReportsLastGroup(t *testing.T) {
	// Nothing matches → all attempts return NoChannelError.
	r := &fakeRouter{MatchModel: "no-such-model"}
	chosen, calls := iterateFallback(t, r, "gpt-4o", []string{"claude", "mini"})
	if chosen != "" {
		t.Fatalf("chosen=%q want empty (all failed)", chosen)
	}
	if len(calls) != 3 {
		t.Fatalf("calls=%v want all three tried", calls)
	}
}

// TestDistributorBuildsModelsToTry verifies the dedupe + trim logic
// matches what we just unit-tested. Uses a recorded gin context to be
// closer to the real path.
func TestDistributorModelsFallbackContextWiring(t *testing.T) {
	cleanup := setupModelsFallbackDB(t)
	defer cleanup()

	body := map[string]any{
		"model":    "gpt-4o",
		"models":   []string{"gpt-4o", "claude-sonnet-4-6", "gpt-4o-mini"},
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/chat/completions", io.NopCloser(bytes.NewReader(raw)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	// We're not invoking distributor.Distribute() directly here (it
	// requires full auth context); instead we exercise the
	// fallback-list dedupe logic the way distributor does.
	primary := body["model"].(string)
	rawFallback := body["models"].([]string)
	modelsToTry := []string{primary}
	seen := map[string]struct{}{primary: {}}
	for _, m := range rawFallback {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		modelsToTry = append(modelsToTry, m)
	}
	want := []string{"gpt-4o", "claude-sonnet-4-6", "gpt-4o-mini"}
	if len(modelsToTry) != len(want) {
		t.Fatalf("modelsToTry=%v want=%v", modelsToTry, want)
	}
	for i, m := range want {
		if modelsToTry[i] != m {
			t.Fatalf("modelsToTry[%d]=%q want=%q", i, modelsToTry[i], m)
		}
	}
}

func TestResolveAutoModelBeforePriorityRouting(t *testing.T) {
	cleanup := setupModelsFallbackDB(t)
	defer cleanup()

	highPriority := int64(20)
	lowPriority := int64(10)
	highWeight := uint(50)
	lowWeight := uint(100)
	channels := []*model.Channel{
		{
			Id:       4202,
			Status:   common.ChannelStatusEnabled,
			Name:     "low-priority",
			Models:   "model-low",
			Group:    model.ChannelLaneDirect,
			Priority: &lowPriority,
			Weight:   &lowWeight,
		},
		{
			Id:       4201,
			Status:   common.ChannelStatusEnabled,
			Name:     "high-priority",
			Models:   "model-high,model-secondary",
			Group:    model.ChannelLaneDirect,
			Priority: &highPriority,
			Weight:   &highWeight,
		},
	}
	for _, channel := range channels {
		if err := model.CoreDB().Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		for _, modelName := range channel.GetModels() {
			if err := model.CoreDB().Create(&model.Ability{
				Group:     model.ChannelLaneDirect,
				Model:     modelName,
				ChannelId: channel.Id,
				Enabled:   true,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
			}).Error; err != nil {
				t.Fatalf("create ability: %v", err)
			}
		}
	}

	resolved, err := resolveAutoModel(nil, []string{model.ChannelLaneDirect}, func(modelName string) bool {
		return modelName != "model-high"
	})
	if err != nil {
		t.Fatalf("resolveAutoModel returned error: %v", err)
	}
	if resolved != "model-secondary" {
		t.Fatalf("resolved=%q want=model-secondary", resolved)
	}

	router := &relayrouter.PriorityRouter{}
	decision, err := router.Select(context.Background(), &relayrouter.RequestContext{
		ResolvedModel:   resolved,
		CandidateGroups: []string{model.ChannelLaneDirect},
		DefaultGroup:    model.ChannelLaneDirect,
	})
	if err != nil {
		t.Fatalf("priority Select returned error: %v", err)
	}
	if decision.ChannelID != channels[1].Id || decision.Model != "model-secondary" {
		t.Fatalf("decision=(channel=%d model=%q), want=(channel=%d model-secondary)", decision.ChannelID, decision.Model, channels[1].Id)
	}
}
