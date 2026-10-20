package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/packages/contracts/openapi"
)

const (
	defaultReleaseNotesPrimaryModel  = "claude-haiku-5-5"
	defaultReleaseNotesFallbackModel = "deepseek-flash"
	defaultReleaseNotesBaseURL       = "http://lazu-backend:3000/v1"
	releaseNotesMaxTokens            = 2400
	releaseNoteOutputContract        = `Return one JSON object with exactly this shape and all four locales present: {"user_visible":true,"kind":"release_note","default_locale":"zh","title_i18n":{"zh":"...","en":"...","zh-TW":"...","ja":"..."},"summary_i18n":{"zh":"...","en":"...","zh-TW":"...","ja":"..."},"body_i18n":{"zh":["..."],"en":["..."],"zh-TW":["..."],"ja":["..."]},"api_changes":[]}. Each body_i18n locale must contain 1 to 3 short items. Fill api_changes only for real new, changed, or deprecated public API operations or fields, using {"method":"GET","path":"/public/path/{id}","note_i18n":{"zh":"...","en":"...","zh-TW":"...","ja":"..."}}. Use an empty array otherwise. Do not put API paths in the localized prose. Do not omit locales, add keys, or wrap the JSON in Markdown. If nothing in the range changes what users see or can do, return only {"user_visible":false}.`
	releaseNoteAudienceGuide         = `Write only about what users of the Lazu console and API notice: new models or features, price changes, changed behaviour, and fixes they would have run into. Leave out refactors, tests, CI, dependency updates, internal tooling and self-hosting internals. Name models plainly, never as vendor/model paths.`
)

type ReleaseNoteGenerationRequest struct {
	CommitSHA         string
	PreviousCommitSHA string
	Version           string
	CommitSummary     string
	Delivery          string
	Draft             bool
	// MinInterval skips generation while the last published release note is
	// younger than this. The range still starts at that note, so the skipped
	// commits go into the next one.
	MinInterval time.Duration
}

// errNothingUserVisible marks a range the model judged invisible to users.
var errNothingUserVisible = errors.New("no user-visible changes")

// ReleaseDeploymentRecordRequest records a successful production deployment
// even when it intentionally has no user-facing announcement.
type ReleaseDeploymentRecordRequest struct {
	CommitSHA         string
	PreviousCommitSHA string
	Version           string
}

type releaseNoteModelOutput struct {
	UserVisible   *bool             `json:"user_visible"`
	Kind          string            `json:"kind"`
	DefaultLocale string            `json:"default_locale"`
	TitleI18N     map[string]string `json:"title_i18n"`
	SummaryI18N   map[string]string `json:"summary_i18n"`
	BodyI18N      map[string]any    `json:"body_i18n"`
	APIChanges    []model.APIChange `json:"api_changes"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

var releaseNoteDeniedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsk-[a-z0-9]{10,}`),
	regexp.MustCompile(`(?i)\b(redis|postgres|database|routing_outcome|routing_decision|channel\s*#?\d+|gross_margin|upstream_cost)\b`),
	regexp.MustCompile(`\b[0-9a-f]{7,40}\b`),
	regexp.MustCompile(`/[A-Za-z0-9_.\-/]+`),
}

var requiredReleaseNoteLocales = []string{"zh", "en", "zh-TW", "ja"}

func ReleaseNotesModelName() string {
	modelName := strings.TrimSpace(os.Getenv("RELEASE_NOTES_PRIMARY_MODEL"))
	if modelName == "" {
		// RELEASE_NOTES_MODEL remains a compatibility alias for existing
		// deployments. New installations should use the explicit primary name.
		modelName = strings.TrimSpace(os.Getenv("RELEASE_NOTES_MODEL"))
	}
	if modelName == "" {
		return defaultReleaseNotesPrimaryModel
	}
	return modelName
}

func releaseNotesModelCandidates() []string {
	primary := ReleaseNotesModelName()
	fallback, configured := os.LookupEnv("RELEASE_NOTES_FALLBACK_MODEL")
	if configured {
		fallback = strings.TrimSpace(fallback)
	} else {
		fallback = defaultReleaseNotesFallbackModel
	}
	candidates := []string{primary}
	if fallback != "" && fallback != primary {
		candidates = append(candidates, fallback)
	}
	return candidates
}

func preparedReleaseDeployment(req ReleaseDeploymentRecordRequest) (*model.ReleaseDeployment, error) {
	req.CommitSHA = strings.TrimSpace(req.CommitSHA)
	req.PreviousCommitSHA = strings.TrimSpace(req.PreviousCommitSHA)
	req.Version = strings.TrimSpace(req.Version)
	if req.CommitSHA == "" {
		return nil, errors.New("commit sha is required")
	}
	if req.Version == "" {
		req.Version = req.CommitSHA
	}
	if req.PreviousCommitSHA == "" {
		existing, err := model.ReleaseDeploymentForCommit(req.CommitSHA)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			req.PreviousCommitSHA = existing.PreviousCommitSHA
		} else if latest, err := model.LatestReleaseDeployment(); err != nil {
			return nil, err
		} else if latest != nil {
			req.PreviousCommitSHA = latest.CommitSHA
		}
	}

	return &model.ReleaseDeployment{
		Version:           req.Version,
		CommitSHA:         req.CommitSHA,
		PreviousCommitSHA: req.PreviousCommitSHA,
		DeployedAt:        time.Now().UTC(),
		ReleaseNoteStatus: model.ReleaseNoteStatusSkipped,
	}, nil
}

func RecordReleaseDeployment(req ReleaseDeploymentRecordRequest) (*model.ReleaseDeployment, error) {
	deployment, err := preparedReleaseDeployment(req)
	if err != nil {
		return nil, err
	}
	deployment.ReleaseNoteError = "announcement mode is silent"
	deployment, _, err = model.RecordReleaseDeploymentWithAnnouncement(deployment, nil)
	return deployment, err
}

func GenerateAndRecordReleaseNote(ctx context.Context, req ReleaseNoteGenerationRequest) (*model.ReleaseDeployment, *model.Announcement, error) {
	deployment, err := preparedReleaseDeployment(ReleaseDeploymentRecordRequest{
		CommitSHA:         req.CommitSHA,
		PreviousCommitSHA: req.PreviousCommitSHA,
		Version:           req.Version,
	})
	if err != nil {
		return nil, nil, err
	}
	if existingDeployment, existingAnnouncement, err := model.GeneratedReleaseDeploymentForRange(deployment.PreviousCommitSHA, deployment.CommitSHA); err != nil {
		return nil, nil, err
	} else if existingDeployment != nil && existingAnnouncement != nil {
		// A retry with the same delivery only refreshes the version, so a rerun
		// deploy of an auto-published changelog does not trip the published guard.
		updatedDeployment, updatedAnnouncement, err := model.RecordReleaseDeploymentWithAnnouncement(deployment, &model.AnnouncementUpsert{
			Delivery:    req.Delivery,
			DeliverySet: existingAnnouncement.Delivery != req.Delivery,
		})
		if err != nil {
			return nil, nil, err
		}
		// A changelog draft left by an older worker publishes on its retry.
		if !req.Draft && updatedAnnouncement != nil && updatedAnnouncement.PublishedAt == nil && updatedAnnouncement.UnpublishedAt == nil {
			if updatedAnnouncement, err = model.PublishAnnouncementDraft(updatedAnnouncement.ID); err != nil {
				return nil, nil, err
			}
		}
		return updatedDeployment, updatedAnnouncement, nil
	}
	req.CommitSummary = strings.TrimSpace(req.CommitSummary)

	if recent, err := releaseNotePublishedWithin(req.MinInterval); err != nil {
		return nil, nil, err
	} else if recent {
		deployment.ReleaseNoteError = fmt.Sprintf("a release note was published in the last %s", req.MinInterval)
		return model.RecordReleaseDeploymentWithAnnouncement(deployment, nil)
	}

	if req.CommitSummary == "" {
		deployment.ReleaseNoteError = "empty commit summary"
		return model.RecordReleaseDeploymentWithAnnouncement(deployment, nil)
	}
	apiKey := strings.TrimSpace(os.Getenv("RELEASE_NOTES_API_KEY"))
	if apiKey == "" {
		deployment.ReleaseNoteError = "RELEASE_NOTES_API_KEY is not configured"
		return model.RecordReleaseDeploymentWithAnnouncement(deployment, nil)
	}

	output, sourceModel, err := generateValidatedReleaseNote(ctx, req.CommitSummary, apiKey)
	if errors.Is(err, errNothingUserVisible) {
		deployment.ReleaseNoteError = err.Error()
		return model.RecordReleaseDeploymentWithAnnouncement(deployment, nil)
	}
	if err != nil {
		deployment.ReleaseNoteStatus = model.ReleaseNoteStatusFailed
		deployment.ReleaseNoteError = err.Error()
		return model.RecordReleaseDeploymentWithAnnouncement(deployment, nil)
	}

	announcementInput := &model.AnnouncementUpsert{
		Kind:             model.AnnouncementKindReleaseNote,
		TitleI18N:        output.TitleI18N,
		SummaryI18N:      output.SummaryI18N,
		BodyI18N:         output.BodyI18N,
		Version:          deployment.Version,
		Audiences:        &[]string{model.AnnouncementAudienceConsole, model.AnnouncementAudienceDocs},
		APIChanges:       &output.APIChanges,
		DefaultLocale:    output.DefaultLocale,
		Source:           model.AnnouncementSourceDeploy,
		Delivery:         req.Delivery,
		DeliverySet:      true,
		SourceModel:      sourceModel,
		SourceCommitFrom: deployment.PreviousCommitSHA,
		SourceCommitTo:   deployment.CommitSHA,
		Published:        !req.Draft,
	}
	return model.RecordReleaseDeploymentWithAnnouncement(deployment, announcementInput)
}

func LatestReleaseDeploymentCommitSHA() (string, error) {
	latest, err := model.LatestReleaseDeployment()
	if err != nil || latest == nil {
		return "", err
	}
	return latest.CommitSHA, nil
}

func releaseNotePublishedWithin(interval time.Duration) (bool, error) {
	if interval <= 0 {
		return false, nil
	}
	publishedAt, err := model.LatestPublishedDeployReleaseNoteAt()
	if err != nil || publishedAt == nil {
		return false, err
	}
	return time.Since(*publishedAt) < interval, nil
}

// LatestPublishedReleaseNoteCommitSHA is the commit the last published release
// note ran up to. Silent releases do not move it, so the deploy script can start
// the next note there instead of losing the commits those releases shipped.
func LatestPublishedReleaseNoteCommitSHA() (string, error) {
	latest, err := model.LatestGeneratedReleaseDeployment()
	if err != nil || latest == nil {
		return "", err
	}
	return latest.CommitSHA, nil
}

func generateValidatedReleaseNote(ctx context.Context, summary string, apiKey string) (*releaseNoteModelOutput, string, error) {
	errorsByModel := make([]string, 0, 2)
	for _, modelName := range releaseNotesModelCandidates() {
		output, err := callReleaseNoteModel(ctx, summary, apiKey, modelName)
		if err == nil {
			err = validateReleaseNoteOutput(output)
		}
		if err == nil {
			return output, modelName, nil
		}
		if errors.Is(err, errNothingUserVisible) {
			return nil, modelName, err
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		errorsByModel = append(errorsByModel, fmt.Sprintf("%s: %v", modelName, err))
	}
	return nil, "", fmt.Errorf("release note generation failed across configured models: %s", strings.Join(errorsByModel, "; "))
}

func callReleaseNoteModel(ctx context.Context, summary string, apiKey string, modelName string) (*releaseNoteModelOutput, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("RELEASE_NOTES_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = defaultReleaseNotesBaseURL
	}
	payload := map[string]any{
		"model": modelName,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You write concise user-facing Lazu release notes. " + releaseNoteAudienceGuide + " " + releaseNoteOutputContract + " Do not mention internal file paths, hashes, database, Redis, queues, provider accounts, channel IDs, costs, or margins.",
			},
			{
				"role":    "user",
				"content": "Summarize this deployed commit range for normal users. Include non-empty title_i18n, summary_i18n, and body_i18n values for every required locale: zh, en, zh-TW, and ja. Keep each locale short and return only the JSON object, or {\"user_visible\":false} if users would notice none of it.\n\n" + summary,
			},
		},
		"temperature":     0.2,
		"max_tokens":      releaseNotesMaxTokens,
		"response_format": releaseNoteResponseFormat(modelName),
	}
	// The gateway turns reasoning_effort into Claude thinking, which a short
	// summary does not need.
	if !strings.HasPrefix(strings.ToLower(modelName), "claude-") {
		payload["reasoning_effort"] = "low"
	}
	if isGeminiReleaseNoteModel(modelName) {
		payload["extra_body"] = map[string]any{
			"google": map[string]any{
				"thinking_config": map[string]any{
					"thinking_level": "low",
				},
			},
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("release note model returned status %d", resp.StatusCode)
	}
	var completion chatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return nil, err
	}
	if len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return nil, errors.New("release note model returned empty content")
	}
	var output releaseNoteModelOutput
	content := stripJSONFence(completion.Choices[0].Message.Content)
	if err := json.Unmarshal([]byte(content), &output); err != nil {
		finishReason := strings.TrimSpace(completion.Choices[0].FinishReason)
		if finishReason == "" {
			finishReason = "unknown"
		}
		return nil, fmt.Errorf("decode release note model output (finish_reason=%s, completion_tokens=%d): %w", finishReason, completion.Usage.CompletionTokens, err)
	}
	return &output, nil
}

// stripJSONFence unwraps a ```json block, which Claude models sometimes add
// around a JSON-only answer.
func stripJSONFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimPrefix(content, "json")
	content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	return strings.TrimSpace(content)
}

func releaseNoteResponseFormat(modelName string) map[string]any {
	if !isGeminiReleaseNoteModel(modelName) {
		return map[string]any{"type": "json_object"}
	}
	localizedString := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"zh":    map[string]any{"type": "string"},
			"en":    map[string]any{"type": "string"},
			"zh-TW": map[string]any{"type": "string"},
			"ja":    map[string]any{"type": "string"},
		},
		"required": requiredReleaseNoteLocales,
	}
	localizedBody := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"zh":    releaseNoteBodySchema(),
			"en":    releaseNoteBodySchema(),
			"zh-TW": releaseNoteBodySchema(),
			"ja":    releaseNoteBodySchema(),
		},
		"required": requiredReleaseNoteLocales,
	}
	apiChange := map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"method": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "note_i18n": localizedString},
		"required":   []string{"method", "path", "note_i18n"},
	}
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "lazu_release_note",
			"strict": true,
			"schema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"user_visible":   map[string]any{"type": "boolean"},
					"kind":           map[string]any{"type": "string", "enum": []string{"release_note"}},
					"default_locale": map[string]any{"type": "string", "enum": requiredReleaseNoteLocales},
					"title_i18n":     localizedString,
					"summary_i18n":   localizedString,
					"body_i18n":      localizedBody,
					"api_changes":    map[string]any{"type": "array", "items": apiChange},
				},
				"required": []string{"user_visible", "kind", "default_locale", "title_i18n", "summary_i18n", "body_i18n", "api_changes"},
			},
		},
	}
}

func isGeminiReleaseNoteModel(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "gemini-")
}

func releaseNoteBodySchema() map[string]any {
	return map[string]any{
		"type":     "array",
		"items":    map[string]any{"type": "string"},
		"minItems": 1,
		"maxItems": 3,
	}
}

func validateReleaseNoteOutput(output *releaseNoteModelOutput) error {
	if output == nil {
		return errors.New("empty release note output")
	}
	if output.UserVisible != nil && !*output.UserVisible {
		return errNothingUserVisible
	}
	output.Kind = model.AnnouncementKindReleaseNote
	output.DefaultLocale = model.NormalizeAnnouncementLocale(output.DefaultLocale)
	if output.DefaultLocale == "" {
		output.DefaultLocale = "zh"
	}
	if len(output.TitleI18N) == 0 || len(output.BodyI18N) == 0 {
		return errors.New("release note output missing title or body")
	}
	normalizedTitle := map[string]string{}
	for locale, text := range output.TitleI18N {
		normalizedLocale := model.NormalizeAnnouncementLocale(locale)
		text = strings.TrimSpace(text)
		if text != "" {
			normalizedTitle[normalizedLocale] = text
		}
	}
	normalizedSummary := map[string]string{}
	for locale, text := range output.SummaryI18N {
		normalizedLocale := model.NormalizeAnnouncementLocale(locale)
		text = strings.TrimSpace(text)
		if text != "" {
			normalizedSummary[normalizedLocale] = text
		}
	}
	normalizedBody := map[string]any{}
	for locale, value := range output.BodyI18N {
		normalizedLocale := model.NormalizeAnnouncementLocale(locale)
		lines := releaseNoteBodyLines(value)
		if len(lines) > 3 {
			return fmt.Errorf("release note output locale %s has %d body items; expected 1 to 3", normalizedLocale, len(lines))
		}
		if len(lines) > 0 {
			normalizedBody[normalizedLocale] = lines
		}
	}
	output.TitleI18N = normalizedTitle
	output.SummaryI18N = normalizedSummary
	output.BodyI18N = normalizedBody
	for _, locale := range requiredReleaseNoteLocales {
		if output.TitleI18N[locale] == "" || output.SummaryI18N[locale] == "" || len(releaseNoteBodyLines(output.BodyI18N[locale])) == 0 {
			return fmt.Errorf("release note output missing required locale %s", locale)
		}
	}
	raw, _ := json.Marshal([]any{output.TitleI18N, output.SummaryI18N, output.BodyI18N})
	for _, pattern := range releaseNoteDeniedPatterns {
		if pattern.Match(raw) {
			return fmt.Errorf("release note output contains internal or sensitive wording")
		}
	}
	output.APIChanges = ValidateAPIChanges(output.APIChanges)
	return nil
}

// ValidateAPIChanges drops entries absent from the public OpenAPI contract.
// Callers may show a validation error if an admin supplied any invalid entry.
func ValidateAPIChanges(changes []model.APIChange) []model.APIChange {
	valid := make([]model.APIChange, 0, len(changes))
	for _, change := range changes {
		change.Method = strings.ToUpper(strings.TrimSpace(change.Method))
		change.Path = strings.TrimSpace(change.Path)
		if !openapi.HasPublicOperation(change.Method, change.Path) {
			log.Print("release note: dropping API change outside public contract")
			continue
		}
		validNotes := true
		for _, locale := range requiredReleaseNoteLocales {
			note := strings.TrimSpace(change.NoteI18N[locale])
			if note == "" {
				validNotes = false
				break
			}
			for _, pattern := range releaseNoteDeniedPatterns {
				if pattern.MatchString(note) {
					validNotes = false
					break
				}
			}
		}
		if !validNotes {
			log.Print("release note: dropping API change with invalid localized note")
			continue
		}
		valid = append(valid, change)
	}
	return valid
}

func releaseNoteBodyLines(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []string:
		return typed
	case []any:
		lines := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				lines = append(lines, text)
			}
		}
		return lines
	default:
		if value == nil {
			return nil
		}
		return []string{fmt.Sprint(value)}
	}
}
