package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

// Anthropic's input_tokens leaves cache reads and writes out. A pool that
// caches on its side billed the whole prompt all the same; reading only
// input_tokens made the billing check see 3 tokens for a 1,600-token prompt
// and the long-context check call a full long input truncated (a coding-agent
// pool, 2026-10-01).
func TestObservedPromptTokensCountsAnthropicCache(t *testing.T) {
	openai := &dto.Usage{PromptTokens: 1660, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 1600}}
	if got := observedPromptTokens(openai); got != 1660 {
		t.Fatalf("OpenAI usage = %d, want prompt_tokens as is", got)
	}
	anthropic := &dto.Usage{PromptTokens: 3, PromptExcludesCache: true,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 1600, CachedCreationTokens: 57}}
	if got := observedPromptTokens(anthropic); got != 1660 {
		t.Fatalf("Anthropic usage = %d, want input + cache read + cache write = 1660", got)
	}
	split := &dto.Usage{PromptTokens: 3, PromptExcludesCache: true,
		PromptTokensDetails: dto.InputTokenDetails{CachedCreation5mTokens: 40, CachedCreation1hTokens: 17}}
	if got := observedPromptTokens(split); got != 60 {
		t.Fatalf("split cache writes = %d, want 60", got)
	}
	if observedPromptTokens(nil) != 0 {
		t.Fatal("nil usage")
	}
}

// addWatchedModel puts a second model of the relay under watch, with a
// probe record that keeps failing for the same reason.
func addWatchedModel(t *testing.T, db *gorm.DB, name string, started time.Time, failures int, errorMessage string) {
	t.Helper()
	if err := db.Model(&model.Channel{}).Where("id = ?", 40).Update("models", "gpt-6-sol,"+name).Error; err != nil {
		t.Fatalf("models: %v", err)
	}
	if err := db.Create(&model.Ability{
		Group: model.ChannelLaneAgent, Model: name, ChannelId: 40, Enabled: true, Role: model.AbilityRoleInventory,
	}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	cell := model.RouteObservationCell{Lane: model.ChannelLaneAgent, ModelName: name, ChannelId: 40}
	if err := model.StartRouteObservations(nil, []model.RouteObservationCell{cell}, started); err != nil {
		t.Fatalf("start: %v", err)
	}
	probes := make([]model.ChannelProbeResult, 0, failures)
	for i := 0; i < failures; i++ {
		probes = append(probes, model.ChannelProbeResult{
			Lane: cell.Lane, ModelName: name, ChannelId: 40,
			ProbedAt: time.Now().UTC().Add(-time.Duration(i+1) * time.Hour), ErrorMessage: errorMessage,
		})
	}
	if len(probes) > 0 {
		if err := db.Create(&probes).Error; err != nil {
			t.Fatalf("seed probes: %v", err)
		}
	}
}

func getObservationDetail(t *testing.T, id string) (int, struct {
	Data struct {
		Slots  int                `json:"slots"`
		Routes []observationRoute `json:"routes"`
	} `json:"data"`
}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/model_ops/sources/"+id+"/observation", nil)
	GetModelOpsSourceObservation(c)
	var payload struct {
		Data struct {
			Slots  int                `json:"slots"`
			Routes []observationRoute `json:"routes"`
		} `json:"data"`
	}
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return recorder.Code, payload
}

// The dialog draws each watched model's observation: its probes in two-hour slots,
// its checks day by day, and — for a model with a problem — why.
func TestSourceObservationDetailDrawsEachModelsObservation(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	if code, _ := getObservationDetail(t, "ch-relay"); code != http.StatusNotFound {
		t.Fatalf("nothing watched: status = %d, want 404", code)
	}
	started := time.Now().UTC().Add(-(2*24 + 3) * time.Hour)
	seedPassedObservation(t, db, started)
	addWatchedModel(t, db, "claude-haiku-4-5", started, 30, "This model is busy right now. Please retry in a moment.")

	code, detail := getObservationDetail(t, "ch-relay")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	// Six test days of two-hour slots.
	if detail.Data.Slots != 72 || len(detail.Data.Routes) != 2 {
		t.Fatalf("slots = %d routes = %d, want 72 and 2", detail.Data.Slots, len(detail.Data.Routes))
	}
	clean, broken := detail.Data.Routes[0], detail.Data.Routes[1]
	if clean.Model != "gpt-6-sol" || clean.Verdict != observationVerdictWatching {
		t.Fatalf("first = %s %s, want the clean model still watching", clean.Model, clean.Verdict)
	}
	if broken.Model != "claude-haiku-4-5" || broken.Verdict != observationVerdictProblem ||
		broken.Reason == nil || broken.Reason.Kind != "probe" || !strings.Contains(broken.Reason.Detail, "busy") {
		t.Fatalf("second = %+v, want a probe problem with the upstream's reason", broken)
	}
	var samples, failures int64
	for _, slot := range broken.Slots {
		samples += slot.Samples
		failures += slot.Failures
	}
	if samples != 30 || failures != 30 {
		t.Fatalf("slots hold %d samples, %d failures; want 30 and 30", samples, failures)
	}
	wantSlots := int(time.Since(broken.SlotOrigin)/(2*time.Hour)) + 1
	if len(broken.Slots) != wantSlots {
		t.Fatalf("slots = %d, want one per elapsed slot = %d", len(broken.Slots), wantSlots)
	}
	if len(clean.Days) != 3 || clean.Days[2][model.ObservationCheckBilling] != observationStatePass || len(clean.Days[0]) != 0 {
		t.Fatalf("days = %+v, want three days with today's checks passed", clean.Days)
	}
}

// The ledger names the model holding a source back instead of showing the
// worst cell of all of them.
func TestSourceSummaryNamesTheProblemModel(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	started := time.Now().UTC().Add(-2 * 24 * time.Hour)
	seedPassedObservation(t, db, started)
	addWatchedModel(t, db, "claude-haiku-4-5", started, 30, "This model is busy right now.")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/model_ops/sources", nil)
	GetModelOpsSources(c)
	var payload struct {
		Data struct {
			Items []modelOpsSource `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %v", recorder.Code, err)
	}
	var summary *observationSummary
	for _, item := range payload.Data.Items {
		if item.ID == "ch-relay" && item.Observation != nil {
			summary = item.Observation.Summary
		}
	}
	if summary == nil || summary.AllPassed != 1 || summary.ProblemCount != 1 || summary.Pending != 0 ||
		len(summary.Problems) != 1 || summary.Problems[0].Model != "claude-haiku-4-5" || summary.Problems[0].Kind != "probe" {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.WorstProbe == nil || summary.WorstProbe.Model != "claude-haiku-4-5" || summary.WorstProbe.Failures != summary.WorstProbe.Samples {
		t.Fatalf("worst probe = %+v, want the dead model named", summary.WorstProbe)
	}
}

func postSourceAction(id, action, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/model_ops/sources/"+id+"/"+action, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return recorder
}

func abilityRole(t *testing.T, db *gorm.DB, name string) string {
	t.Helper()
	var ability model.Ability
	if err := db.Where("channel_id = ? AND model = ?", 40, name).First(&ability).Error; err != nil {
		return ""
	}
	return ability.Role
}

// Going live can name the models: the ready one goes, the one still being
// watched stays, and asking for an unready or unknown model refuses.
func TestGoLiveSelectedModels(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	seedPassedObservation(t, db, time.Now().Add(-8*24*time.Hour))
	addWatchedModel(t, db, "claude-haiku-4-5", time.Now().Add(-24*time.Hour), 0, "")

	if recorder := postSourceAction("ch-relay", "go_live", `{"role":"backup","models":["claude-haiku-4-5"]}`, GoLiveModelOpsSource); recorder.Code != http.StatusConflict {
		t.Fatalf("unready model: status = %d, want 409", recorder.Code)
	}
	if recorder := postSourceAction("ch-relay", "go_live", `{"role":"backup","models":["nope"]}`, GoLiveModelOpsSource); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown model: status = %d, want 404", recorder.Code)
	}
	if recorder := postSourceAction("ch-relay", "go_live", `{"role":"backup","models":["gpt-6-sol"]}`, GoLiveModelOpsSource); recorder.Code != http.StatusOK {
		t.Fatalf("ready model: status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if got := abilityRole(t, db, "gpt-6-sol"); got != model.AbilityRoleBackup {
		t.Fatalf("gpt-6-sol role = %q, want backup", got)
	}
	if got := abilityRole(t, db, "claude-haiku-4-5"); got != model.AbilityRoleInventory {
		t.Fatalf("haiku role = %q, want inventory", got)
	}
}

// Removing takes a watched model off the source; a model that already serves
// cannot be removed this way.
func TestRemoveWatchedModels(t *testing.T) {
	db := setupRouteObservationControllerTestDB(t)
	seedPassedObservation(t, db, time.Now().Add(-8*24*time.Hour))
	addWatchedModel(t, db, "claude-haiku-4-5", time.Now().Add(-24*time.Hour), 3, "busy")
	if recorder := postSourceAction("ch-relay", "go_live", `{"role":"backup","models":["gpt-6-sol"]}`, GoLiveModelOpsSource); recorder.Code != http.StatusOK {
		t.Fatalf("go live: %d", recorder.Code)
	}
	if recorder := postSourceAction("ch-relay", "remove_models", `{"models":["gpt-6-sol"]}`, RemoveModelOpsSourceModels); recorder.Code == http.StatusOK {
		t.Fatal("a serving model must not be removable from the observation dialog")
	}
	if recorder := postSourceAction("ch-relay", "remove_models", `{"models":[]}`, RemoveModelOpsSourceModels); recorder.Code == http.StatusOK {
		t.Fatal("an empty removal must be refused")
	}
	if recorder := postSourceAction("ch-relay", "remove_models", `{"models":["claude-haiku-4-5"]}`, RemoveModelOpsSourceModels); recorder.Code != http.StatusOK {
		t.Fatalf("remove: status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var channel model.Channel
	if err := db.First(&channel, 40).Error; err != nil {
		t.Fatal(err)
	}
	if channel.Models != "gpt-6-sol" {
		t.Fatalf("models = %q, want gpt-6-sol", channel.Models)
	}
	if got := abilityRole(t, db, "claude-haiku-4-5"); got != "" {
		t.Fatalf("haiku ability survived with role %q", got)
	}
	if got := abilityRole(t, db, "gpt-6-sol"); got != model.AbilityRoleBackup {
		t.Fatalf("gpt-6-sol role = %q, want backup kept", got)
	}
	var watching int64
	db.Model(&model.RouteObservation{}).Where("channel_id = ? AND model_name = ?", 40, "claude-haiku-4-5").Count(&watching)
	if watching != 0 {
		t.Fatal("observation of a removed model must end")
	}
}

// Midway, one early failure is room the observation can still absorb; a route
// that fails so often that a clean remainder cannot reach the bar is a problem
// now. The bar and the window are testSourceChecks: 96% over six days.
func TestObservationProbeHopeless(t *testing.T) {
	useTestSourceChecks(t)
	now := time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC)
	started := now.Add(-(37*time.Hour + 30*time.Minute))
	view := func(samples, failures int64, complete bool) observationView {
		rate, state := observationSuccessState(samples, failures)
		return observationView{StartedAt: started, Samples: samples, Failures: failures, SuccessRate: rate, SuccessState: state, Complete: complete}
	}
	if observationProbeHopeless(view(37, 1, false), now) {
		t.Fatal("36/37 on day 2 can still reach 96% by the end of the observation")
	}
	if !observationProbeHopeless(view(37, 36, false), now) {
		t.Fatal("1/37 on day 2 cannot reach 96%")
	}
	if !observationProbeHopeless(view(144, 9, true), now) {
		t.Fatal("a finished observation below the bar is a problem")
	}
	if observationProbeHopeless(view(10, 3, false), now) {
		t.Fatal("too few samples to judge is not a problem yet")
	}
}
