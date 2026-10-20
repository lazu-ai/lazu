package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func setupReleaseNotesTestDB(t *testing.T) {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	previousDB := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if err := db.AutoMigrate(&model.Announcement{}, &model.AnnouncementUserState{}, &model.ReleaseDeployment{}); err != nil {
		t.Fatalf("migrate release notes tables: %v", err)
	}
}

func TestGenerateAndRecordReleaseNoteSkipsWithoutAPIKey(t *testing.T) {
	setupReleaseNotesTestDB(t)
	t.Setenv("RELEASE_NOTES_API_KEY", "")

	deployment, announcement, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		Version:           "v1",
		CommitSummary:     "- add status page",
	})
	if err != nil {
		t.Fatalf("generate release note without api key: %v", err)
	}
	if announcement != nil {
		t.Fatalf("expected no announcement without api key: %#v", announcement)
	}
	if deployment.ReleaseNoteStatus != model.ReleaseNoteStatusSkipped {
		t.Fatalf("expected skipped deployment, got %#v", deployment)
	}
	if !strings.Contains(deployment.ReleaseNoteError, "RELEASE_NOTES_API_KEY") {
		t.Fatalf("expected api key error, got %q", deployment.ReleaseNoteError)
	}
}

func TestReleaseNoteAPIChangesUsePublicContractOnly(t *testing.T) {
	notes := map[string]string{"zh": "更新", "en": "Updated", "zh-TW": "更新", "ja": "更新"}
	newEndpoints := ValidateAPIChanges([]model.APIChange{
		{Method: "GET", Path: "/api/public/changelog", NoteI18N: notes},
		{Method: "GET", Path: "/api/public/changelog.md", NoteI18N: notes},
		{Method: "POST", Path: "/api/public/changelog", NoteI18N: notes},
	})
	if len(newEndpoints) != 2 {
		t.Fatalf("new public routes missing from contract: %#v", newEndpoints)
	}
	output := &releaseNoteModelOutput{
		TitleI18N:   map[string]string{"zh": "标题", "en": "Title", "zh-TW": "標題", "ja": "タイトル"},
		SummaryI18N: map[string]string{"zh": "摘要", "en": "Summary", "zh-TW": "摘要", "ja": "概要"},
		BodyI18N:    map[string]any{"zh": []string{"内容"}, "en": []string{"Body"}, "zh-TW": []string{"內容"}, "ja": []string{"本文"}},
		APIChanges:  []model.APIChange{{Method: "get", Path: "/api/usage/requests/{id}", NoteI18N: notes}, {Method: "GET", Path: "/internal/secrets", NoteI18N: notes}, {Method: "POST", Path: "/api/usage/requests/{id}", NoteI18N: notes}},
	}
	if err := validateReleaseNoteOutput(output); err != nil {
		t.Fatal(err)
	}
	if len(output.APIChanges) != 1 || output.APIChanges[0].Method != "GET" {
		t.Fatalf("validated changes: %#v", output.APIChanges)
	}
	output.BodyI18N["en"] = []string{"GET /api/status"}
	if err := validateReleaseNoteOutput(output); err == nil {
		t.Fatal("prose path must remain prohibited")
	}
}

func TestRecordReleaseDeploymentKeepsSilentDeploymentsOutOfAnnouncements(t *testing.T) {
	setupReleaseNotesTestDB(t)

	deployment, err := RecordReleaseDeployment(ReleaseDeploymentRecordRequest{
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		Version:           "v0.7.4",
	})
	if err != nil {
		t.Fatalf("record silent deployment: %v", err)
	}
	if deployment.ReleaseNoteStatus != model.ReleaseNoteStatusSkipped {
		t.Fatalf("silent deployment should skip announcements, got %#v", deployment)
	}
	if !strings.Contains(deployment.ReleaseNoteError, "silent") {
		t.Fatalf("silent deployment reason missing: %q", deployment.ReleaseNoteError)
	}
	var announcementCount int64
	if err := model.CoreDB().Model(&model.Announcement{}).Count(&announcementCount).Error; err != nil {
		t.Fatalf("count announcements: %v", err)
	}
	if announcementCount != 0 {
		t.Fatalf("silent deployment created %d announcements", announcementCount)
	}
}

func TestGenerateAndRecordReleaseNoteRetryUpdatesExistingDraftDelivery(t *testing.T) {
	setupReleaseNotesTestDB(t)
	deployment, announcement, err := model.RecordReleaseDeploymentWithAnnouncement(&model.ReleaseDeployment{
		Version:           "v0.7.4",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		ReleaseNoteStatus: model.ReleaseNoteStatusSkipped,
	}, &model.AnnouncementUpsert{
		Kind:             model.AnnouncementKindReleaseNote,
		TitleI18N:        map[string]string{"zh": "发布说明"},
		SummaryI18N:      map[string]string{"zh": "摘要"},
		BodyI18N:         map[string]any{"zh": []string{"内容"}},
		DefaultLocale:    "zh",
		Source:           model.AnnouncementSourceDeploy,
		Delivery:         model.AnnouncementDeliveryNotice,
		DeliverySet:      true,
		SourceCommitFrom: "1234567",
		SourceCommitTo:   "abcdef1",
	})
	if err != nil || announcement == nil {
		t.Fatalf("create generated draft: deployment=%#v announcement=%#v err=%v", deployment, announcement, err)
	}

	// No API key is configured: the retry must still update the existing draft
	// rather than making its delivery correction depend on model availability.
	t.Setenv("RELEASE_NOTES_API_KEY", "")
	retryDeployment, retryAnnouncement, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		Version:           "v0.7.4",
		Delivery:          model.AnnouncementDeliveryCritical,
		Draft:             true,
	})
	if err != nil || retryAnnouncement == nil {
		t.Fatalf("retry generated draft: deployment=%#v announcement=%#v err=%v", retryDeployment, retryAnnouncement, err)
	}
	if retryDeployment.ID != deployment.ID || retryAnnouncement.ID != announcement.ID || retryAnnouncement.Delivery != model.AnnouncementDeliveryCritical {
		t.Fatalf("retry should preserve the draft and update delivery, deployment=%#v announcement=%#v", retryDeployment, retryAnnouncement)
	}
}

func TestReleaseNotesModelNameDefaultsToHaiku55(t *testing.T) {
	t.Setenv("RELEASE_NOTES_PRIMARY_MODEL", "")
	t.Setenv("RELEASE_NOTES_MODEL", "")
	if got := ReleaseNotesModelName(); got != "claude-haiku-5-5" {
		t.Fatalf("unexpected default release notes model: %q", got)
	}
}

func TestReleaseNotesModelCandidatesUseExplicitPrimaryAndFallback(t *testing.T) {
	t.Setenv("RELEASE_NOTES_PRIMARY_MODEL", "gemini-primary")
	t.Setenv("RELEASE_NOTES_MODEL", "legacy-model")
	t.Setenv("RELEASE_NOTES_FALLBACK_MODEL", "deepseek-fallback")
	models := releaseNotesModelCandidates()
	if len(models) != 2 || models[0] != "gemini-primary" || models[1] != "deepseek-fallback" {
		t.Fatalf("unexpected release-note model candidates: %#v", models)
	}
}

func TestReleaseNotesModelCandidatesCanDisableOrDeduplicateFallback(t *testing.T) {
	t.Setenv("RELEASE_NOTES_PRIMARY_MODEL", "gemini-primary")
	t.Setenv("RELEASE_NOTES_FALLBACK_MODEL", "")
	if models := releaseNotesModelCandidates(); len(models) != 1 || models[0] != "gemini-primary" {
		t.Fatalf("empty fallback should disable failover: %#v", models)
	}
	t.Setenv("RELEASE_NOTES_FALLBACK_MODEL", "gemini-primary")
	if models := releaseNotesModelCandidates(); len(models) != 1 || models[0] != "gemini-primary" {
		t.Fatalf("duplicate fallback should be removed: %#v", models)
	}
}

func TestGenerateAndRecordReleaseNoteFallsBackAfterMalformedPrimaryOutput(t *testing.T) {
	setupReleaseNotesTestDB(t)
	models := make([]string, 0, 2)
	formats := make([]string, 0, 2)
	thinkingLevels := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode model request: %v", err)
		}
		models = append(models, fmt.Sprint(request["model"]))
		responseFormat, _ := request["response_format"].(map[string]any)
		formats = append(formats, fmt.Sprint(responseFormat["type"]))
		thinkingLevel := ""
		if extraBody, ok := request["extra_body"].(map[string]any); ok {
			if googleBody, ok := extraBody["google"].(map[string]any); ok {
				if thinkingConfig, ok := googleBody["thinking_config"].(map[string]any); ok {
					thinkingLevel = fmt.Sprint(thinkingConfig["thinking_level"])
				}
			}
		}
		thinkingLevels = append(thinkingLevels, thinkingLevel)
		content := `{"kind":`
		finishReason := "length"
		completionTokens := releaseNotesMaxTokens
		if request["model"] == "deepseek-v4-flash" {
			content = `{"kind":"release_note","default_locale":"zh","title_i18n":{"zh":"可靠性更新","en":"Reliability update","zh-TW":"可靠性更新","ja":"信頼性の更新"},"summary_i18n":{"zh":"公告生成更加可靠","en":"Release notes are more reliable","zh-TW":"公告產生更加可靠","ja":"リリースノートの信頼性が向上しました"},"body_i18n":{"zh":["失败现在会被明确报告"],"en":["Failures are now reported clearly"],"zh-TW":["失敗現在會被明確回報"],"ja":["失敗が明確に報告されるようになりました"]}}`
			finishReason = "stop"
			completionTokens = 600
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"content": content},
				"finish_reason": finishReason,
			}},
			"usage": map[string]int{"completion_tokens": completionTokens},
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("RELEASE_NOTES_API_KEY", "test-key")
	t.Setenv("RELEASE_NOTES_BASE_URL", server.URL+"/v1")
	t.Setenv("RELEASE_NOTES_PRIMARY_MODEL", "gemini-3.7-flash")
	t.Setenv("RELEASE_NOTES_FALLBACK_MODEL", "deepseek-v4-flash")

	deployment, announcement, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		Version:           "v1",
		CommitSummary:     "- improve release note reliability",
		Delivery:          model.AnnouncementDeliveryChangelog,
		Draft:             true,
	})
	if err != nil {
		t.Fatalf("generate release note after fallback: %v", err)
	}
	if strings.Join(models, ",") != "gemini-3.7-flash,deepseek-v4-flash" {
		t.Fatalf("unexpected model attempt order: %#v", models)
	}
	if strings.Join(formats, ",") != "json_schema,json_object" {
		t.Fatalf("unexpected response formats: %#v", formats)
	}
	if strings.Join(thinkingLevels, ",") != "low," {
		t.Fatalf("native Gemini thinking level should only be sent to Gemini: %#v", thinkingLevels)
	}
	if deployment.ReleaseNoteStatus != model.ReleaseNoteStatusGenerated || announcement == nil {
		t.Fatalf("expected generated announcement after retry, deployment=%#v announcement=%#v", deployment, announcement)
	}
	if announcement.SourceModel != "deepseek-v4-flash" {
		t.Fatalf("fallback source model was not recorded: %#v", announcement)
	}
	if announcement.Delivery != model.AnnouncementDeliveryChangelog {
		t.Fatalf("unexpected generated announcement delivery: %#v", announcement)
	}
	if announcement.Version != "v1" || string(announcement.Audiences) != `["console","docs"]` || announcement.PublishedAt != nil {
		t.Fatalf("generated draft must retain version and both audiences: %#v", announcement)
	}
	if announcement.PublishedAt != nil {
		t.Fatalf("deployment announcement should stay a draft until reviewed: %#v", announcement)
	}
}

func TestGenerateAndRecordReleaseNoteDoesNotCallFallbackAfterPrimarySuccess(t *testing.T) {
	setupReleaseNotesTestDB(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"content": `{"kind":"release_note","default_locale":"zh","title_i18n":{"zh":"更新","en":"Update","zh-TW":"更新","ja":"更新"},"summary_i18n":{"zh":"可靠性提升","en":"Improved reliability","zh-TW":"可靠性提升","ja":"信頼性を改善"},"body_i18n":{"zh":["运行更稳定"],"en":["More stable operation"],"zh-TW":["運行更穩定"],"ja":["動作が安定しました"]}}`},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("RELEASE_NOTES_API_KEY", "test-key")
	t.Setenv("RELEASE_NOTES_BASE_URL", server.URL+"/v1")
	t.Setenv("RELEASE_NOTES_PRIMARY_MODEL", "gemini-3.7-flash")
	t.Setenv("RELEASE_NOTES_FALLBACK_MODEL", "deepseek-v4-flash")

	_, announcement, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA: "abcdef2", PreviousCommitSHA: "abcdef1", Version: "v2",
		CommitSummary: "- improve release note reliability", Delivery: model.AnnouncementDeliveryChangelog, Draft: true,
	})
	if err != nil || announcement == nil {
		t.Fatalf("generate release note with primary: announcement=%#v err=%v", announcement, err)
	}
	if attempts != 1 || announcement.SourceModel != "gemini-3.7-flash" {
		t.Fatalf("primary success should not call fallback: attempts=%d announcement=%#v", attempts, announcement)
	}
}

const releaseNoteFixtureJSON = `{"user_visible":true,"kind":"release_note","default_locale":"zh","title_i18n":{"zh":"更新","en":"Update","zh-TW":"更新","ja":"更新"},"summary_i18n":{"zh":"新增模型","en":"New model","zh-TW":"新增模型","ja":"新モデル"},"body_i18n":{"zh":["新增视频模型"],"en":["A new video model"],"zh-TW":["新增影片模型"],"ja":["新しい動画モデル"]},"api_changes":[]}`

// releaseNoteModelServer answers every chat completion with content and
// records the request bodies it saw.
func releaseNoteModelServer(t *testing.T, content string) *[]map[string]any {
	t.Helper()
	requests := &[]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*requests = append(*requests, body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}, "finish_reason": "stop"}},
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("RELEASE_NOTES_API_KEY", "test-key")
	t.Setenv("RELEASE_NOTES_BASE_URL", server.URL+"/v1")
	t.Setenv("RELEASE_NOTES_PRIMARY_MODEL", "claude-haiku-5-5")
	t.Setenv("RELEASE_NOTES_FALLBACK_MODEL", "deepseek-v4-flash")
	return requests
}

func TestGenerateAndRecordReleaseNoteReadsFencedClaudeJSONWithoutThinking(t *testing.T) {
	setupReleaseNotesTestDB(t)
	requests := releaseNoteModelServer(t, "```json\n"+releaseNoteFixtureJSON+"\n```")

	_, announcement, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA: "abcdef2", PreviousCommitSHA: "abcdef1", Version: "v2",
		CommitSummary: "- feat(video): add a model", Delivery: model.AnnouncementDeliveryChangelog,
	})
	if err != nil || announcement == nil || announcement.PublishedAt == nil {
		t.Fatalf("a changelog note should publish: announcement=%#v err=%v", announcement, err)
	}
	if len(*requests) != 1 || announcement.SourceModel != "claude-haiku-5-5" {
		t.Fatalf("fenced JSON should be read from the primary: requests=%d announcement=%#v", len(*requests), announcement)
	}
	if _, sent := (*requests)[0]["reasoning_effort"]; sent {
		t.Fatalf("a Claude request should not ask for thinking: %#v", (*requests)[0])
	}
}

func TestGenerateAndRecordReleaseNoteSkipsRangesWithoutUserVisibleChanges(t *testing.T) {
	setupReleaseNotesTestDB(t)
	requests := releaseNoteModelServer(t, `{"user_visible":false}`)

	deployment, announcement, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA: "abcdef2", PreviousCommitSHA: "abcdef1", Version: "v2",
		CommitSummary: "- refactor(relay): split the adaptor", Delivery: model.AnnouncementDeliveryChangelog,
	})
	if err != nil || announcement != nil {
		t.Fatalf("nothing user-visible should record without a note: announcement=%#v err=%v", announcement, err)
	}
	if len(*requests) != 1 {
		t.Fatalf("an answer of nothing user-visible is not a failure to retry: requests=%d", len(*requests))
	}
	if deployment.ReleaseNoteStatus != model.ReleaseNoteStatusSkipped || deployment.ReleaseNoteError != "no user-visible changes" {
		t.Fatalf("unexpected deployment record: %#v", deployment)
	}
}

func TestGenerateAndRecordReleaseNoteHoldsWhileTheLastNoteIsRecent(t *testing.T) {
	setupReleaseNotesTestDB(t)
	requests := releaseNoteModelServer(t, releaseNoteFixtureJSON)
	request := func(commit string) ReleaseNoteGenerationRequest {
		return ReleaseNoteGenerationRequest{
			CommitSHA: commit, Version: commit, CommitSummary: "- feat(video): add a model",
			Delivery: model.AnnouncementDeliveryChangelog, MinInterval: 24 * time.Hour,
		}
	}

	if _, first, err := GenerateAndRecordReleaseNote(context.Background(), request("sha-1")); err != nil || first == nil {
		t.Fatalf("the first note should publish: announcement=%#v err=%v", first, err)
	}
	deployment, held, err := GenerateAndRecordReleaseNote(context.Background(), request("sha-2"))
	if err != nil || held != nil || len(*requests) != 1 {
		t.Fatalf("a second note within a day should wait without a model call: announcement=%#v requests=%d err=%v", held, len(*requests), err)
	}
	if !strings.Contains(deployment.ReleaseNoteError, "published in the last 24h") {
		t.Fatalf("the hold should say why: %#v", deployment)
	}
	start, err := LatestPublishedReleaseNoteCommitSHA()
	if err != nil || start != "sha-1" {
		t.Fatalf("the held release must not move the next note's start, got %q err=%v", start, err)
	}
}

func TestGenerateAndRecordReleaseNoteRetryPublishesAnOlderChangelogDraft(t *testing.T) {
	setupReleaseNotesTestDB(t)
	_, draft, err := model.RecordReleaseDeploymentWithAnnouncement(&model.ReleaseDeployment{
		Version: "v0.7.4", CommitSHA: "abcdef1", PreviousCommitSHA: "1234567",
	}, &model.AnnouncementUpsert{
		Kind:          model.AnnouncementKindReleaseNote,
		TitleI18N:     map[string]string{"zh": "发布说明"},
		SummaryI18N:   map[string]string{"zh": "摘要"},
		BodyI18N:      map[string]any{"zh": []string{"内容"}},
		DefaultLocale: "zh",
		Source:        model.AnnouncementSourceDeploy,
		Delivery:      model.AnnouncementDeliveryChangelog,
		DeliverySet:   true,
	})
	if err != nil || draft == nil || draft.PublishedAt != nil {
		t.Fatalf("create changelog draft: announcement=%#v err=%v", draft, err)
	}

	t.Setenv("RELEASE_NOTES_API_KEY", "")
	_, retried, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA: "abcdef1", PreviousCommitSHA: "1234567", Version: "v0.7.4",
		Delivery: model.AnnouncementDeliveryChangelog,
	})
	if err != nil || retried == nil || retried.ID != draft.ID || retried.PublishedAt == nil {
		t.Fatalf("a changelog retry should publish the existing draft: announcement=%#v err=%v", retried, err)
	}
}

func TestReleaseNoteHoldUsesTheLatestPublicationTime(t *testing.T) {
	setupReleaseNotesTestDB(t)
	// The newer deployment's note went out two days ago; an older deployment's
	// draft was published an hour ago. The hold must see the hour.
	twoDaysAgo := time.Now().UTC().Add(-48 * time.Hour)
	anHourAgo := time.Now().UTC().Add(-time.Hour)
	for _, row := range []struct {
		commit      string
		deployedAt  time.Time
		publishedAt time.Time
	}{
		{"sha-older", twoDaysAgo.Add(-time.Hour), anHourAgo},
		{"sha-newer", twoDaysAgo, twoDaysAgo},
	} {
		publishedAt := row.publishedAt
		if _, _, err := model.RecordReleaseDeploymentWithAnnouncement(&model.ReleaseDeployment{
			Version: row.commit, CommitSHA: row.commit, PreviousCommitSHA: row.commit + "-before", DeployedAt: row.deployedAt,
		}, &model.AnnouncementUpsert{
			Kind:           model.AnnouncementKindReleaseNote,
			TitleI18N:      map[string]string{"zh": "发布说明"},
			SummaryI18N:    map[string]string{"zh": "摘要"},
			BodyI18N:       map[string]any{"zh": []string{"内容"}},
			DefaultLocale:  "zh",
			Source:         model.AnnouncementSourceDeploy,
			Delivery:       model.AnnouncementDeliveryChangelog,
			DeliverySet:    true,
			PublishedAt:    &publishedAt,
			PublishedAtSet: true,
		}); err != nil {
			t.Fatalf("record %s: %v", row.commit, err)
		}
	}
	held, err := releaseNotePublishedWithin(6 * time.Hour)
	if err != nil || !held {
		t.Fatalf("a note published an hour ago should hold the next one: held=%v err=%v", held, err)
	}
}

func TestGenerateAndRecordReleaseNoteRecordsBothModelFailures(t *testing.T) {
	setupReleaseNotesTestDB(t)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	t.Setenv("RELEASE_NOTES_API_KEY", "test-key")
	t.Setenv("RELEASE_NOTES_BASE_URL", server.URL+"/v1")
	t.Setenv("RELEASE_NOTES_PRIMARY_MODEL", "gemini-3.7-flash")
	t.Setenv("RELEASE_NOTES_FALLBACK_MODEL", "deepseek-v4-flash")

	deployment, announcement, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA: "abcdef3", PreviousCommitSHA: "abcdef2", Version: "v3",
		CommitSummary: "- improve release note reliability", Delivery: model.AnnouncementDeliveryChangelog, Draft: true,
	})
	if err != nil || announcement != nil {
		t.Fatalf("failed generation should be recorded without an announcement: deployment=%#v announcement=%#v err=%v", deployment, announcement, err)
	}
	if attempts != 2 || deployment.ReleaseNoteStatus != model.ReleaseNoteStatusFailed {
		t.Fatalf("both configured models should be attempted: attempts=%d deployment=%#v", attempts, deployment)
	}
	if !strings.Contains(deployment.ReleaseNoteError, "gemini-3.7-flash") || !strings.Contains(deployment.ReleaseNoteError, "deepseek-v4-flash") {
		t.Fatalf("failure record should identify both attempted models: %q", deployment.ReleaseNoteError)
	}
}

func TestLatestReleaseDeploymentCommitSHAReturnsLastLiveDeployment(t *testing.T) {
	setupReleaseNotesTestDB(t)
	generatedAt := model.JSONValue([]byte(`{"zh":"发布说明"}`))
	body := model.JSONValue([]byte(`{"zh":["已生成"]}`))
	publishedAt := time.Date(2026, 5, 30, 3, 0, 0, 0, time.UTC)
	announcement := model.Announcement{
		Kind:          model.AnnouncementKindReleaseNote,
		TitleI18N:     generatedAt,
		SummaryI18N:   generatedAt,
		BodyI18N:      body,
		DefaultLocale: "zh",
		Source:        model.AnnouncementSourceDeploy,
		PublishedAt:   &publishedAt,
	}
	if err := model.CoreDB().Create(&announcement).Error; err != nil {
		t.Fatalf("create release note announcement: %v", err)
	}
	announcementID := announcement.ID
	deployments := []model.ReleaseDeployment{
		{
			Version:                   "generated",
			CommitSHA:                 "sha-generated",
			PreviousCommitSHA:         "sha-before",
			DeployedAt:                publishedAt,
			ReleaseNoteStatus:         model.ReleaseNoteStatusGenerated,
			ReleaseNoteAnnouncementID: &announcementID,
		},
		{
			Version:           "rollback",
			CommitSHA:         "sha-rollback",
			PreviousCommitSHA: "sha-generated",
			DeployedAt:        publishedAt.Add(time.Hour),
			ReleaseNoteStatus: model.ReleaseNoteStatusSkipped,
			ReleaseNoteError:  "announcement mode is silent",
		},
	}
	if err := model.CoreDB().Create(&deployments).Error; err != nil {
		t.Fatalf("create release deployments: %v", err)
	}
	sha, err := LatestReleaseDeploymentCommitSHA()
	if err != nil {
		t.Fatalf("latest release deployment commit sha: %v", err)
	}
	if sha != "sha-rollback" {
		t.Fatalf("expected last live deployment sha, got %q", sha)
	}
}

func TestLatestPublishedReleaseNoteCommitSHASkipsSilentReleases(t *testing.T) {
	setupReleaseNotesTestDB(t)
	if sha, err := LatestPublishedReleaseNoteCommitSHA(); err != nil || sha != "" {
		t.Fatalf("no published note should give an empty sha, got %q err=%v", sha, err)
	}
	deployment, announcement, err := model.RecordReleaseDeploymentWithAnnouncement(&model.ReleaseDeployment{
		Version:           "v0.7.10",
		CommitSHA:         "sha-announced",
		PreviousCommitSHA: "sha-before",
		DeployedAt:        time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}, &model.AnnouncementUpsert{
		Kind:          model.AnnouncementKindReleaseNote,
		TitleI18N:     map[string]string{"zh": "发布说明"},
		SummaryI18N:   map[string]string{"zh": "摘要"},
		BodyI18N:      map[string]any{"zh": []string{"内容"}},
		DefaultLocale: "zh",
		Source:        model.AnnouncementSourceDeploy,
		Delivery:      model.AnnouncementDeliveryChangelog,
		DeliverySet:   true,
		Published:     true,
	})
	if err != nil || deployment == nil || announcement == nil {
		t.Fatalf("record published note: deployment=%#v announcement=%#v err=%v", deployment, announcement, err)
	}
	for _, sha := range []string{"sha-silent-1", "sha-silent-2"} {
		if _, err := RecordReleaseDeployment(ReleaseDeploymentRecordRequest{CommitSHA: sha, Version: sha}); err != nil {
			t.Fatalf("record silent release %s: %v", sha, err)
		}
	}

	published, err := LatestPublishedReleaseNoteCommitSHA()
	if err != nil || published != "sha-announced" {
		t.Fatalf("silent releases must not move the published note, got %q err=%v", published, err)
	}
	live, err := LatestReleaseDeploymentCommitSHA()
	if err != nil || live != "sha-silent-2" {
		t.Fatalf("expected the last live release, got %q err=%v", live, err)
	}
}

func TestGenerateAndRecordReleaseNoteRetryKeepsPublishedChangelog(t *testing.T) {
	setupReleaseNotesTestDB(t)
	_, published, err := model.RecordReleaseDeploymentWithAnnouncement(&model.ReleaseDeployment{
		Version:           "v0.7.4",
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
	}, &model.AnnouncementUpsert{
		Kind:             model.AnnouncementKindReleaseNote,
		TitleI18N:        map[string]string{"zh": "发布说明"},
		SummaryI18N:      map[string]string{"zh": "摘要"},
		BodyI18N:         map[string]any{"zh": []string{"内容"}},
		DefaultLocale:    "zh",
		Source:           model.AnnouncementSourceDeploy,
		Delivery:         model.AnnouncementDeliveryChangelog,
		DeliverySet:      true,
		SourceCommitFrom: "1234567",
		SourceCommitTo:   "abcdef1",
		Published:        true,
	})
	if err != nil || published == nil || published.PublishedAt == nil {
		t.Fatalf("create published changelog: announcement=%#v err=%v", published, err)
	}

	// A rerun deploy of the same release asks for the same delivery: it only
	// refreshes the version and must not fail on the published guard.
	t.Setenv("RELEASE_NOTES_API_KEY", "")
	deployment, retried, err := GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		Version:           "v0.7.5",
		Delivery:          model.AnnouncementDeliveryChangelog,
	})
	if err != nil || retried == nil || retried.ID != published.ID || retried.PublishedAt == nil || deployment.Version != "v0.7.5" {
		t.Fatalf("rerun should keep the published changelog, deployment=%#v announcement=%#v err=%v", deployment, retried, err)
	}

	// Raising a published entry to a notice still needs a new reviewed announcement.
	_, _, err = GenerateAndRecordReleaseNote(context.Background(), ReleaseNoteGenerationRequest{
		CommitSHA:         "abcdef1",
		PreviousCommitSHA: "1234567",
		Version:           "v0.7.5",
		Delivery:          model.AnnouncementDeliveryNotice,
		Draft:             true,
	})
	if err == nil || !strings.Contains(err.Error(), "already published") {
		t.Fatalf("delivery change on a published note should be refused, got %v", err)
	}
}

func TestGenerateAndRecordReleaseNoteReusesRecordedRangeForReversion(t *testing.T) {
	setupReleaseNotesTestDB(t)
	t.Setenv("RELEASE_NOTES_API_KEY", "")
	previous := &model.ReleaseDeployment{
		Version:           "v1.2.3",
		CommitSHA:         "sha-current",
		PreviousCommitSHA: "sha-before",
		ReleaseNoteStatus: model.ReleaseNoteStatusSkipped,
	}
	if _, _, err := model.RecordReleaseDeploymentWithAnnouncement(previous, nil); err != nil {
		t.Fatalf("record initial deployment: %v", err)
	}

	deployment, announcement, err := GenerateAndRecordReleaseNote(t.Context(), ReleaseNoteGenerationRequest{
		CommitSHA:     "sha-current",
		Version:       "v1.2.4",
		Delivery:      model.AnnouncementDeliveryNotice,
		CommitSummary: "- Reversion metadata refresh",
		Draft:         true,
	})
	if err != nil || deployment == nil || announcement != nil {
		t.Fatalf("missing API key should record the existing range without an announcement, deployment=%#v announcement=%#v err=%v", deployment, announcement, err)
	}
	if deployment.PreviousCommitSHA != "sha-before" || deployment.Version != "v1.2.4" {
		t.Fatalf("reversion should reuse original range and refresh version, got %#v", deployment)
	}
}

func TestValidateReleaseNoteOutputNormalizesAndRedacts(t *testing.T) {
	output := &releaseNoteModelOutput{
		DefaultLocale: "zh-CN",
		TitleI18N: map[string]string{
			"zh-CN": "状态页上线", "en-US": "Status page is live",
			"zh-TW": "狀態頁上線", "ja": "ステータスページを公開",
		},
		SummaryI18N: map[string]string{
			"zh-CN": "新的状态入口", "en-US": "A new status surface",
			"zh-TW": "新的狀態入口", "ja": "新しいステータス画面",
		},
		BodyI18N: map[string]any{
			"zh-CN": []any{"用户可以查看服务状态"}, "en-US": []any{"Users can see service status"},
			"zh-TW": []any{"使用者可以查看服務狀態"}, "ja": []any{"サービス状態を確認できます"},
		},
	}
	if err := validateReleaseNoteOutput(output); err != nil {
		t.Fatalf("validate release note output: %v", err)
	}
	if output.DefaultLocale != "zh" || output.TitleI18N["zh"] == "" || output.TitleI18N["en"] == "" {
		t.Fatalf("locales were not normalized: %#v", output)
	}
	if _, ok := output.TitleI18N["zh-CN"]; ok {
		t.Fatalf("non-canonical locale should not remain: %#v", output.TitleI18N)
	}

	sensitive := &releaseNoteModelOutput{
		DefaultLocale: "en",
		TitleI18N:     map[string]string{"zh": "内部更新", "en": "Internal update", "zh-TW": "內部更新", "ja": "内部更新"},
		SummaryI18N:   map[string]string{"zh": "内部更新", "en": "Internal update", "zh-TW": "內部更新", "ja": "内部更新"},
		BodyI18N: map[string]any{
			"zh": []string{"Changed channel #12 routing"}, "en": []string{"Changed channel #12 routing"},
			"zh-TW": []string{"Changed channel #12 routing"}, "ja": []string{"Changed channel #12 routing"},
		},
	}
	if err := validateReleaseNoteOutput(sensitive); err == nil {
		t.Fatalf("sensitive release note output should be rejected")
	}
}

func TestValidateReleaseNoteOutputRequiresAllLocales(t *testing.T) {
	output := &releaseNoteModelOutput{
		DefaultLocale: "en",
		TitleI18N:     map[string]string{"en": "Update"},
		SummaryI18N:   map[string]string{"en": "Summary"},
		BodyI18N:      map[string]any{"en": []string{"Body"}},
	}
	if err := validateReleaseNoteOutput(output); err == nil || !strings.Contains(err.Error(), "required locale") {
		t.Fatalf("expected required locale error, got %v", err)
	}
}

func TestValidateReleaseNoteOutputRejectsMoreThanThreeBodyItems(t *testing.T) {
	output := &releaseNoteModelOutput{
		DefaultLocale: "en",
		TitleI18N:     map[string]string{"zh": "更新", "en": "Update", "zh-TW": "更新", "ja": "更新"},
		SummaryI18N:   map[string]string{"zh": "摘要", "en": "Summary", "zh-TW": "摘要", "ja": "概要"},
		BodyI18N: map[string]any{
			"zh":    []string{"一", "二", "三", "四"},
			"en":    []string{"One"},
			"zh-TW": []string{"一"},
			"ja":    []string{"一"},
		},
	}
	if err := validateReleaseNoteOutput(output); err == nil || !strings.Contains(err.Error(), "expected 1 to 3") {
		t.Fatalf("expected body item limit error, got %v", err)
	}
}

func TestReleaseNoteOutputContractRequiresFourLocales(t *testing.T) {
	for _, locale := range requiredReleaseNoteLocales {
		if !strings.Contains(releaseNoteOutputContract, fmt.Sprintf("%q", locale)) {
			t.Fatalf("output contract missing locale %q: %s", locale, releaseNoteOutputContract)
		}
	}
}
