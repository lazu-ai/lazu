package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

// fakeArk serves the Ark task API: a submission answers with a task id, and
// the task reads "running" once before the scripted terminal answer.
type fakeArk struct {
	mu        sync.Mutex
	submitted map[string]any
	submitAs  int
	terminal  string
	reads     int
}

func (f *fakeArk) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &f.submitted)
		if f.submitAs != 0 {
			w.WriteHeader(f.submitAs)
			_, _ = w.Write([]byte(`{"error":{"code":"InvalidParameter","message":"refused"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"cgt-canary"}`))
		return
	}
	f.reads++
	if f.reads == 1 {
		_, _ = w.Write([]byte(`{"id":"cgt-canary","status":"running"}`))
		return
	}
	_, _ = w.Write([]byte(f.terminal))
}

func setupVideoCanaryTest(t *testing.T, ark *fakeArk) (*model.Channel, VideoCanaryCandidate) {
	t.Helper()
	db := setupModelOpsControllerTestDB(t)
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
	if err := db.Create(&model.User{Id: 1, Username: "root", Status: common.UserStatusEnabled, Role: common.RoleRootUser}).Error; err != nil {
		t.Fatalf("seed root user: %v", err)
	}
	previous := videoCanaryPollInterval
	videoCanaryPollInterval = time.Millisecond
	t.Cleanup(func() { videoCanaryPollInterval = previous })
	server := httptest.NewServer(ark)
	t.Cleanup(server.Close)
	baseURL := server.URL + "/api/v3"
	channel := model.Channel{
		Id: 93, PublicID: "ark-canary", Name: "ark", Key: "ark-key", BaseURL: &baseURL,
		Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		OtherSettings: `{"video_family":"ark"}`,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return &channel, VideoCanaryCandidate{Lane: model.ChannelLaneDirect, ModelName: "seedance-2.5", ChannelId: channel.Id}
}

// A canary asks for the cheapest clip the model makes, waits for the
// provider's answer and files it as a generation of the route.
func TestRunVideoCanaryGeneratesTheSmallestClipAndWaits(t *testing.T) {
	ark := &fakeArk{terminal: `{"id":"cgt-canary","status":"succeeded","content":{"video_url":"https://cdn.example.com/c.mp4"}}`}
	_, candidate := setupVideoCanaryTest(t, ark)

	verdict := runVideoCanary(context.Background(), candidate)
	outcome := verdict.outcome
	if !outcome.Success || outcome.Skipped || outcome.Modality != model.ChannelProbeModalityVideo ||
		outcome.CostUSD != channelProbeVideoCanaryCostUSD || outcome.ProbedAt.IsZero() {
		t.Fatalf("canary = %+v, want a successful, costed video generation", outcome)
	}
	if ark.reads < 2 {
		t.Fatalf("canary read the task %d times; it must wait out the running state", ark.reads)
	}
	if ark.submitted["duration"] != float64(4) || ark.submitted["resolution"] != "480p" || ark.submitted["generate_audio"] != false {
		t.Fatalf("canary submitted %v, want 4s of silent 480p", ark.submitted)
	}
}

func TestRunVideoCanaryFilesFailuresByWhoseTheyAre(t *testing.T) {
	cases := []struct {
		name       string
		ark        *fakeArk
		skipped    bool
		generation bool
	}{
		{
			name:       "provider failed to make it",
			ark:        &fakeArk{terminal: `{"status":"failed","error":{"code":"InternalServiceError","message":"boom"}}`},
			generation: true,
		},
		{
			name:    "provider moderated the fixed prompt",
			ark:     &fakeArk{terminal: `{"status":"failed","error":{"code":"OutputVideoSensitiveContentDetected"}}`},
			skipped: true,
		},
		{
			name:    "provider refused the canary's shape",
			ark:     &fakeArk{submitAs: http.StatusBadRequest},
			skipped: true,
		},
		{
			name: "provider down at submission",
			ark:  &fakeArk{submitAs: http.StatusServiceUnavailable},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, candidate := setupVideoCanaryTest(t, tc.ark)
			verdict := runVideoCanary(context.Background(), candidate)
			if verdict.outcome.Success || verdict.outcome.Skipped != tc.skipped || verdict.generation != tc.generation {
				t.Fatalf("canary = %+v generation=%t, want skipped=%t generation=%t",
					verdict.outcome, verdict.generation, tc.skipped, tc.generation)
			}
			if verdict.outcome.ErrorMessage == "" {
				t.Fatal("a canary that did not succeed must say why")
			}
			if tc.ark.submitAs != 0 && verdict.outcome.CostUSD != 0 {
				t.Fatalf("a refused submission costs nothing; recorded $%.2f", verdict.outcome.CostUSD)
			}
		})
	}
}

func TestRunVideoCanarySweepRespectsItsOwnBudget(t *testing.T) {
	t.Setenv("CHANNEL_PROBE_VIDEO_DAILY_BUDGET_USD", "0")
	result, err := RunVideoCanarySweep(context.Background(), time.Now().UTC())
	if err != nil || !result.BudgetHit || result.Probed != 0 {
		t.Fatalf("zero-budget canary sweep = %+v err=%v, want disabled", result, err)
	}
}

func TestVideoCanaryModelsDefaultToSeedanceAndCanBeSwitchedOff(t *testing.T) {
	if models := videoCanaryModels(); !models["seedance-2.5"] || len(models) != 1 {
		t.Fatalf("default canary models = %v, want seedance-2.5 only", models)
	}
	t.Setenv("CHANNEL_PROBE_VIDEO_CANARY_MODELS", "off")
	if models := videoCanaryModels(); len(models) != 0 {
		t.Fatalf("canary models with off = %v, want none", models)
	}
	t.Setenv("CHANNEL_PROBE_VIDEO_CANARY_MODELS", " seedance-2.5 , veo-3 ")
	if models := videoCanaryModels(); !models["seedance-2.5"] || !models["veo-3"] {
		t.Fatalf("canary models = %v", models)
	}
}

func TestLowestVideoResolution(t *testing.T) {
	if got := lowestVideoResolution([]string{"1080p", "480p", "720p"}); got != "480p" {
		t.Fatalf("lowest = %q, want 480p", got)
	}
	if got := lowestVideoResolution([]string{"auto"}); got != "" {
		t.Fatalf("lowest of unparseable = %q, want empty", got)
	}
}

// Canaries go only where nothing else speaks for the route: an allowlisted
// model, a day without one. A route tripped by failed generations is owed one
// hourly, ahead of everything else, because nothing else can end its streak.
func TestSelectVideoCanaryCandidates(t *testing.T) {
	db := setupModelOpsControllerTestDB(t)
	if err := db.AutoMigrate(&model.RouteHealthState{}); err != nil {
		t.Fatalf("migrate route health: %v", err)
	}
	now := time.Now().UTC()
	for _, channel := range []model.Channel{
		{Id: 61, PublicID: "c61", Name: "idle", Key: "k", Status: common.ChannelStatusEnabled},
		{Id: 62, PublicID: "c62", Name: "fresh", Key: "k", Status: common.ChannelStatusEnabled},
		{Id: 63, PublicID: "c63", Name: "tripped", Key: "k", Status: common.ChannelStatusEnabled},
	} {
		if err := db.Create(&channel).Error; err != nil {
			t.Fatalf("seed channel: %v", err)
		}
	}
	for _, ability := range []model.Ability{
		{Group: model.ChannelLaneDirect, Model: "seedance-2.5", ChannelId: 61, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "seedance-2.5", ChannelId: 62, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "seedance-2.5", ChannelId: 63, Enabled: true},
		{Group: model.ChannelLaneDirect, Model: "veo-3", ChannelId: 61, Enabled: true},
	} {
		if err := db.Create(&ability).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}
	for _, channelID := range []int{62, 63} {
		if err := db.Create(&model.ChannelProbeResult{
			Lane: model.ChannelLaneDirect, ModelName: "seedance-2.5", ChannelId: channelID,
			ProbedAt: now.Add(-2 * time.Hour), Success: false, Modality: model.ChannelProbeModalityVideo,
		}).Error; err != nil {
			t.Fatalf("seed canary: %v", err)
		}
	}
	if err := db.Create(&model.RouteHealthState{
		Lane: model.ChannelLaneDirect, ModelName: "seedance-2.5", ChannelId: 63,
		State: model.RouteHealthStateHealthy, ConsecutiveFailures: 3, LastEvidenceAt: now.Add(-2 * time.Hour),
		Reason: model.RouteHealthGenerationReason("InternalServiceError"),
	}).Error; err != nil {
		t.Fatalf("seed tripped route: %v", err)
	}

	candidates, err := SelectVideoCanaryCandidatesContext(context.Background(), now)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	var got []int
	for _, candidate := range candidates {
		if candidate.ModelName != "seedance-2.5" {
			t.Fatalf("canary for %s: only allowlisted models get one", candidate.ModelName)
		}
		got = append(got, candidate.ChannelId)
	}
	if len(got) != 2 || got[0] != 63 || got[1] != 61 {
		t.Fatalf("canary candidates = %v, want the tripped route then the idle one", got)
	}
	if !candidates[0].Recovery {
		t.Fatal("the tripped route's canary is a recovery check")
	}
}
