package model

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AnnouncementKindReleaseNote = "release_note"
	AnnouncementKindOperational = "operational"
	AnnouncementKindMaintenance = "maintenance"
	AnnouncementKindIncident    = "incident"
	AnnouncementKindModelUpdate = "model_update"

	AnnouncementSourceManual        = "manual"
	AnnouncementSourceDeploy        = "deploy"
	AnnouncementSourceLegacySetting = "legacy_console_setting"

	// Delivery controls whether an announcement merely appears in the update
	// history or should ask for a user's attention. Deployments choose this
	// deliberately; it must not be inferred from a commit message.
	AnnouncementDeliveryChangelog = "changelog"
	AnnouncementDeliveryNotice    = "notice"
	AnnouncementDeliveryCritical  = "critical"
	AnnouncementAudienceConsole   = "console"
	AnnouncementAudienceDocs      = "docs"

	ReleaseNoteStatusGenerated = "generated"
	ReleaseNoteStatusFailed    = "failed"
	ReleaseNoteStatusSkipped   = "skipped"

	legacyAnnouncementRetentionDays = 60
)

var supportedAnnouncementLocales = map[string]struct{}{
	"zh":    {},
	"zh-TW": {},
	"en":    {},
	"ja":    {},
}

type Announcement struct {
	ID                 uint      `json:"id" gorm:"primaryKey"`
	Kind               string    `json:"kind" gorm:"type:varchar(32);index;not null"`
	TitleI18N          JSONValue `json:"title_i18n" gorm:"column:title_i18n;type:json"`
	SummaryI18N        JSONValue `json:"summary_i18n" gorm:"column:summary_i18n;type:json"`
	BodyI18N           JSONValue `json:"body_i18n" gorm:"column:body_i18n;type:json"`
	DefaultLocale      string    `json:"default_locale" gorm:"type:varchar(16);not null;default:'zh'"`
	Source             string    `json:"source" gorm:"type:varchar(32);index;not null;default:'manual'"`
	Delivery           string    `json:"delivery" gorm:"type:varchar(16);index;not null;default:'notice'"`
	SourceModel        string    `json:"source_model" gorm:"type:varchar(128)"`
	SourceCommitFrom   string    `json:"source_commit_from" gorm:"type:varchar(64);index"`
	SourceCommitTo     string    `json:"source_commit_to" gorm:"type:varchar(64);index"`
	SourceDeploymentID *uint     `json:"source_deployment_id,omitempty" gorm:"uniqueIndex"`
	Version            string    `json:"version" gorm:"type:varchar(64);index"`
	Audiences          JSONValue `json:"audiences" gorm:"column:audiences;type:json"`
	APIChanges         JSONValue `json:"api_changes" gorm:"column:api_changes;type:json"`
	// Model-update announcements name the models they are about and the day the
	// change lands, so the console can pair the notice with those models.
	AffectedModels JSONValue  `json:"affected_models" gorm:"column:affected_models;type:json"`
	EffectiveDate  *time.Time `json:"effective_date,omitempty" gorm:"index"`
	PublishedAt    *time.Time `json:"published_at,omitempty" gorm:"index"`
	UnpublishedAt  *time.Time `json:"unpublished_at,omitempty" gorm:"index"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type AnnouncementUserState struct {
	ID             uint       `json:"id" gorm:"primaryKey"`
	AnnouncementID uint       `json:"announcement_id" gorm:"not null;uniqueIndex:idx_announcement_user_state"`
	UserID         int        `json:"user_id" gorm:"not null;uniqueIndex:idx_announcement_user_state"`
	FirstShownAt   *time.Time `json:"first_shown_at,omitempty"`
	DismissedAt    *time.Time `json:"dismissed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type ReleaseDeployment struct {
	ID                        uint      `json:"id" gorm:"primaryKey"`
	Version                   string    `json:"version" gorm:"type:varchar(64);index"`
	CommitSHA                 string    `json:"commit_sha" gorm:"type:varchar(64);not null;uniqueIndex:idx_release_deployment_range,priority:2"`
	PreviousCommitSHA         string    `json:"previous_commit_sha" gorm:"type:varchar(64);uniqueIndex:idx_release_deployment_range,priority:1"`
	DeployedAt                time.Time `json:"deployed_at" gorm:"index;not null"`
	ReleaseNoteAnnouncementID *uint     `json:"release_note_announcement_id,omitempty"`
	ReleaseNoteStatus         string    `json:"release_note_status" gorm:"type:varchar(32);index;not null"`
	ReleaseNoteError          string    `json:"release_note_error" gorm:"type:text"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type AnnouncementUpsert struct {
	ID                 uint
	Kind               string
	TitleI18N          map[string]string
	SummaryI18N        map[string]string
	BodyI18N           map[string]any
	DefaultLocale      string
	Source             string
	Delivery           string
	DeliverySet        bool
	SourceModel        string
	SourceCommitFrom   string
	SourceCommitTo     string
	SourceDeploymentID *uint
	Version            string
	Audiences          *[]string
	APIChanges         *[]APIChange
	AffectedModels     []string
	AffectedModelsSet  bool
	EffectiveDate      *time.Time
	EffectiveDateSet   bool
	Published          bool
	PublishedAt        *time.Time
	PublishedAtSet     bool
}

type APIChange struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	NoteI18N map[string]string `json:"note_i18n"`
}

type APIChangeView struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Note     string            `json:"note"`
	NoteI18N map[string]string `json:"note_i18n,omitempty"`
}

type AnnouncementView struct {
	ID             uint              `json:"id"`
	Kind           string            `json:"kind"`
	Version        string            `json:"version,omitempty"`
	Audiences      []string          `json:"audiences"`
	APIChanges     []APIChangeView   `json:"api_changes"`
	Type           string            `json:"type"`
	Title          string            `json:"title"`
	Summary        string            `json:"summary,omitempty"`
	Content        string            `json:"content"`
	Body           []string          `json:"body,omitempty"`
	TitleI18N      map[string]string `json:"title_i18n,omitempty"`
	SummaryI18N    map[string]string `json:"summary_i18n,omitempty"`
	BodyI18N       map[string]any    `json:"body_i18n,omitempty"`
	DefaultLocale  string            `json:"default_locale"`
	PublishDate    time.Time         `json:"publishDate"`
	PublishedAt    *time.Time        `json:"published_at,omitempty"`
	UnpublishedAt  *time.Time        `json:"unpublished_at,omitempty"`
	AffectedModels []string          `json:"affected_models,omitempty"`
	EffectiveDate  *time.Time        `json:"effective_date,omitempty"`
	Source         string            `json:"source,omitempty"`
	Delivery       string            `json:"delivery,omitempty"`
	SourceModel    string            `json:"source_model,omitempty"`
	SourceCommitTo string            `json:"source_commit_to,omitempty"`
	IsUnread       bool              `json:"isUnread"`
	DismissedAt    *time.Time        `json:"dismissed_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func NormalizeAnnouncementLocale(locale string) string {
	normalized := strings.TrimSpace(locale)
	lower := strings.ToLower(strings.ReplaceAll(normalized, "_", "-"))
	switch {
	case lower == "":
		return "zh"
	case strings.HasPrefix(lower, "zh-tw"),
		strings.HasPrefix(lower, "zh-hk"),
		strings.HasPrefix(lower, "zh-hant"),
		strings.HasPrefix(lower, "zh-mo"):
		return "zh-TW"
	case strings.HasPrefix(lower, "zh"):
		return "zh"
	case strings.HasPrefix(lower, "ja"):
		return "ja"
	case strings.HasPrefix(lower, "en"):
		return "en"
	default:
		if _, ok := supportedAnnouncementLocales[normalized]; ok {
			return normalized
		}
		return "en"
	}
}

func normalizeAnnouncementKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case AnnouncementKindOperational:
		return AnnouncementKindOperational
	case AnnouncementKindMaintenance:
		return AnnouncementKindMaintenance
	case AnnouncementKindIncident:
		return AnnouncementKindIncident
	case AnnouncementKindModelUpdate:
		return AnnouncementKindModelUpdate
	default:
		return AnnouncementKindReleaseNote
	}
}

func normalizeAnnouncementSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return AnnouncementSourceManual
	}
	return source
}

func normalizeAnnouncementDelivery(delivery string) string {
	switch strings.TrimSpace(delivery) {
	case AnnouncementDeliveryChangelog:
		return AnnouncementDeliveryChangelog
	case AnnouncementDeliveryCritical:
		return AnnouncementDeliveryCritical
	default:
		// Existing manual and legacy announcements were attention-worthy before
		// delivery levels existed, so preserve that behavior by default.
		return AnnouncementDeliveryNotice
	}
}

func announcementNeedsAttention(delivery string) bool {
	normalized := normalizeAnnouncementDelivery(delivery)
	return normalized == AnnouncementDeliveryNotice || normalized == AnnouncementDeliveryCritical
}

func marshalJSONValue(value any, fallback string) JSONValue {
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" {
		return JSONValue([]byte(fallback))
	}
	return JSONValue(raw)
}

// Model-update announcements store their model list as a JSON array; anything
// malformed degrades to "no models named" rather than failing the whole view.
func stringSliceFromJSON(value JSONValue) []string {
	raw := strings.TrimSpace(string(value))
	if raw == "" || raw == "null" {
		return nil
	}
	var parsed []string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	result := make([]string, 0, len(parsed))
	for _, item := range parsed {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func stringMapFromJSON(value JSONValue) map[string]string {
	result := map[string]string{}
	if len(value) == 0 {
		return result
	}
	_ = json.Unmarshal(value, &result)
	return result
}

func anyMapFromJSON(value JSONValue) map[string]any {
	result := map[string]any{}
	if len(value) == 0 {
		return result
	}
	_ = json.Unmarshal(value, &result)
	return result
}

func localizedString(values map[string]string, locale string, fallbackLocale string) string {
	for _, candidate := range []string{
		NormalizeAnnouncementLocale(locale),
		NormalizeAnnouncementLocale(fallbackLocale),
		"zh",
		"en",
	} {
		if text := strings.TrimSpace(values[candidate]); text != "" {
			return text
		}
	}
	for _, text := range values {
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func localizedBody(values map[string]any, locale string, fallbackLocale string) []string {
	for _, candidate := range []string{
		NormalizeAnnouncementLocale(locale),
		NormalizeAnnouncementLocale(fallbackLocale),
		"zh",
		"en",
	} {
		if body := bodyValueToLines(values[candidate]); len(body) > 0 {
			return body
		}
	}
	for _, value := range values {
		if body := bodyValueToLines(value); len(body) > 0 {
			return body
		}
	}
	return nil
}

func bodyValueToLines(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []string:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				result = append(result, text)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(toString(item)); text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		if value == nil {
			return nil
		}
		if text := strings.TrimSpace(toString(value)); text != "" {
			return []string{text}
		}
	}
	return nil
}

func toString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func (a Announcement) ToView(locale string, state *AnnouncementUserState) AnnouncementView {
	titleI18N := stringMapFromJSON(a.TitleI18N)
	summaryI18N := stringMapFromJSON(a.SummaryI18N)
	bodyI18N := anyMapFromJSON(a.BodyI18N)
	title := localizedString(titleI18N, locale, a.DefaultLocale)
	summary := localizedString(summaryI18N, locale, a.DefaultLocale)
	body := localizedBody(bodyI18N, locale, a.DefaultLocale)
	apiChanges := []APIChangeView{}
	var storedChanges []APIChange
	_ = json.Unmarshal(a.APIChanges, &storedChanges)
	for _, change := range storedChanges {
		apiChanges = append(apiChanges, APIChangeView{Method: change.Method, Path: change.Path, Note: localizedString(change.NoteI18N, locale, a.DefaultLocale), NoteI18N: change.NoteI18N})
	}
	content := summary
	if len(body) > 0 {
		content = strings.Join(body, "\n\n")
	}
	publishDate := a.CreatedAt
	if a.PublishedAt != nil {
		publishDate = *a.PublishedAt
	}
	delivery := normalizeAnnouncementDelivery(a.Delivery)
	view := AnnouncementView{
		ID:             a.ID,
		Kind:           a.Kind,
		Version:        a.Version,
		Audiences:      stringSliceFromJSON(a.Audiences),
		APIChanges:     apiChanges,
		Type:           a.Kind,
		Title:          title,
		Summary:        summary,
		Content:        content,
		Body:           body,
		TitleI18N:      titleI18N,
		SummaryI18N:    summaryI18N,
		BodyI18N:       bodyI18N,
		DefaultLocale:  a.DefaultLocale,
		PublishDate:    publishDate,
		PublishedAt:    a.PublishedAt,
		UnpublishedAt:  a.UnpublishedAt,
		AffectedModels: stringSliceFromJSON(a.AffectedModels),
		EffectiveDate:  a.EffectiveDate,
		Source:         a.Source,
		Delivery:       delivery,
		SourceModel:    a.SourceModel,
		SourceCommitTo: a.SourceCommitTo,
		IsUnread:       announcementNeedsAttention(delivery) && (state == nil || state.DismissedAt == nil),
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
	if state != nil {
		view.DismissedAt = state.DismissedAt
	}
	return view
}

func UpsertAnnouncement(input AnnouncementUpsert) (*Announcement, error) {
	if CoreDB() == nil {
		return nil, errors.New("database is not initialized")
	}
	return upsertAnnouncementWithDB(CoreDB(), input)
}

func upsertAnnouncementWithDB(db *gorm.DB, input AnnouncementUpsert) (*Announcement, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	if input.ID != 0 {
		var existing Announcement
		if err := db.First(&existing, input.ID).Error; err != nil {
			return nil, err
		}
		// The admin review editor only sends fields a human may change. When it
		// publishes a generated release-note draft, retain the deploy provenance
		// instead of replacing it with empty manual-editor values.
		if existing.Source == AnnouncementSourceDeploy && normalizeAnnouncementSource(input.Source) == AnnouncementSourceManual {
			input.Source = existing.Source
			input.SourceModel = existing.SourceModel
			input.SourceCommitFrom = existing.SourceCommitFrom
			input.SourceCommitTo = existing.SourceCommitTo
			input.SourceDeploymentID = existing.SourceDeploymentID
			input.Version = existing.Version
		}
	}
	now := time.Now().UTC()
	defaultLocale := NormalizeAnnouncementLocale(input.DefaultLocale)
	announcement := Announcement{
		ID:                 input.ID,
		Kind:               normalizeAnnouncementKind(input.Kind),
		TitleI18N:          marshalJSONValue(normalizeStringLocaleMap(input.TitleI18N), "{}"),
		SummaryI18N:        marshalJSONValue(normalizeStringLocaleMap(input.SummaryI18N), "{}"),
		BodyI18N:           marshalJSONValue(normalizeAnyLocaleMap(input.BodyI18N), "{}"),
		DefaultLocale:      defaultLocale,
		Source:             normalizeAnnouncementSource(input.Source),
		Delivery:           normalizeAnnouncementDelivery(input.Delivery),
		SourceModel:        strings.TrimSpace(input.SourceModel),
		SourceCommitFrom:   strings.TrimSpace(input.SourceCommitFrom),
		SourceCommitTo:     strings.TrimSpace(input.SourceCommitTo),
		SourceDeploymentID: input.SourceDeploymentID,
		Version:            strings.TrimSpace(input.Version),
		Audiences:          marshalJSONValue(normalizeAnnouncementAudiences(input.Audiences), `["console"]`),
		APIChanges:         marshalJSONValue(input.APIChanges, `[]`),
	}
	if input.AffectedModelsSet {
		announcement.AffectedModels = marshalJSONValue(normalizeAffectedModels(input.AffectedModels), "[]")
	}
	if input.EffectiveDateSet && input.EffectiveDate != nil {
		effectiveDate := input.EffectiveDate.UTC()
		announcement.EffectiveDate = &effectiveDate
	}
	if input.Published || input.PublishedAtSet {
		publishedAt := now
		if input.PublishedAt != nil {
			publishedAt = input.PublishedAt.UTC()
		}
		announcement.PublishedAt = &publishedAt
	}
	if announcement.ID == 0 {
		if err := db.Create(&announcement).Error; err != nil {
			return nil, err
		}
		return &announcement, nil
	}

	updates := map[string]any{
		"kind":                 announcement.Kind,
		"title_i18n":           announcement.TitleI18N,
		"summary_i18n":         announcement.SummaryI18N,
		"body_i18n":            announcement.BodyI18N,
		"default_locale":       announcement.DefaultLocale,
		"source":               announcement.Source,
		"source_model":         announcement.SourceModel,
		"source_commit_from":   announcement.SourceCommitFrom,
		"source_commit_to":     announcement.SourceCommitTo,
		"source_deployment_id": announcement.SourceDeploymentID,
		"updated_at":           now,
	}
	if input.DeliverySet {
		updates["delivery"] = announcement.Delivery
	}
	if input.Version != "" {
		updates["version"] = announcement.Version
	}
	if input.Audiences != nil {
		updates["audiences"] = announcement.Audiences
	}
	if input.APIChanges != nil {
		updates["api_changes"] = announcement.APIChanges
	}
	if input.AffectedModelsSet {
		updates["affected_models"] = announcement.AffectedModels
	}
	if input.EffectiveDateSet {
		updates["effective_date"] = announcement.EffectiveDate
	}
	if input.Published || input.PublishedAtSet {
		updates["published_at"] = announcement.PublishedAt
		updates["unpublished_at"] = nil
	}
	if err := db.Model(&Announcement{}).Where("id = ?", announcement.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := db.First(&announcement, announcement.ID).Error; err != nil {
		return nil, err
	}
	return &announcement, nil
}

func normalizeAnnouncementAudiences(input *[]string) []string {
	result := []string{}
	if input != nil {
		for _, audience := range *input {
			if (audience == AnnouncementAudienceConsole || audience == AnnouncementAudienceDocs) && !slicesContains(result, audience) {
				result = append(result, audience)
			}
		}
	}
	if len(result) == 0 {
		return []string{AnnouncementAudienceConsole}
	}
	return result
}

func slicesContains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func normalizeAffectedModels(input []string) []string {
	result := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, item := range input {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}

func normalizeStringLocaleMap(input map[string]string) map[string]string {
	result := map[string]string{}
	for locale, text := range input {
		normalizedLocale := NormalizeAnnouncementLocale(locale)
		if strings.TrimSpace(text) != "" {
			result[normalizedLocale] = strings.TrimSpace(text)
		}
	}
	return result
}

func normalizeAnyLocaleMap(input map[string]any) map[string]any {
	result := map[string]any{}
	for locale, value := range input {
		normalizedLocale := NormalizeAnnouncementLocale(locale)
		if body := bodyValueToLines(value); len(body) > 0 {
			result[normalizedLocale] = body
		}
	}
	return result
}

func ListPublishedAnnouncements(limit int, locale string, userID int) ([]AnnouncementView, error) {
	if CoreDB() == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var rows []Announcement
	if err := announcementConsoleFilter(CoreDB()).
		Where("published_at IS NOT NULL AND unpublished_at IS NULL").
		Order("published_at DESC").
		Order("id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	states, err := loadAnnouncementStates(userID, rows)
	if err != nil {
		return nil, err
	}
	views := make([]AnnouncementView, 0, len(rows))
	for _, row := range rows {
		views = append(views, row.ToView(locale, states[row.ID]))
	}
	return views, nil
}

func ListUnseenAnnouncements(userID int, locale string, limit int) ([]AnnouncementView, error) {
	if userID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var rows []Announcement
	if err := announcementConsoleFilter(CoreDB().Model(&Announcement{})).
		Select("announcements.*").
		Joins("LEFT JOIN announcement_user_states ON announcement_user_states.announcement_id = announcements.id AND announcement_user_states.user_id = ?", userID).
		Where("announcements.published_at IS NOT NULL AND announcements.unpublished_at IS NULL").
		Where("announcements.delivery IN (?, ?) OR announcements.delivery IS NULL OR announcements.delivery = ''", AnnouncementDeliveryNotice, AnnouncementDeliveryCritical).
		Where("announcement_user_states.id IS NULL OR announcement_user_states.dismissed_at IS NULL").
		Order("announcements.published_at DESC").
		Order("announcements.id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	states, err := loadAnnouncementStates(userID, rows)
	if err != nil {
		return nil, err
	}
	result := make([]AnnouncementView, 0, len(rows))
	now := time.Now().UTC()
	for _, row := range rows {
		item := row.ToView(locale, states[row.ID])
		if !item.IsUnread {
			continue
		}
		result = append(result, item)
		_ = markAnnouncementFirstShown(item.ID, userID, now)
	}
	return result, nil
}

// HasUnseenCriticalAnnouncement is deliberately independent of the bounded
// unseen feed. The version plate uses it to decide whether to open the update
// panel automatically, so a critical item must not be hidden behind newer
// notices that filled the current page.
func HasUnseenCriticalAnnouncement(userID int) (bool, error) {
	item, err := UnseenCriticalAnnouncement(userID, "zh")
	return item != nil, err
}

// UnseenCriticalAnnouncement returns the newest unread critical item without
// applying the ordinary unseen-page limit. It supplies the exact item that
// triggered a critical auto-open, so the dialog never hides that reason behind
// newer routine announcements.
func UnseenCriticalAnnouncement(userID int, locale string) (*AnnouncementView, error) {
	if CoreDB() == nil || userID <= 0 {
		return nil, nil
	}
	var row Announcement
	err := announcementConsoleFilter(CoreDB().Model(&Announcement{})).
		Joins("LEFT JOIN announcement_user_states ON announcement_user_states.announcement_id = announcements.id AND announcement_user_states.user_id = ?", userID).
		Where("announcements.published_at IS NOT NULL AND announcements.unpublished_at IS NULL").
		Where("announcements.delivery = ?", AnnouncementDeliveryCritical).
		Where("announcement_user_states.id IS NULL OR announcement_user_states.dismissed_at IS NULL").
		Order("announcements.published_at DESC").
		Order("announcements.id DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	states, err := loadAnnouncementStates(userID, []Announcement{row})
	if err != nil {
		return nil, err
	}
	item := row.ToView(locale, states[row.ID])
	if !item.IsUnread {
		return nil, nil
	}
	now := time.Now().UTC()
	_ = markAnnouncementFirstShown(item.ID, userID, now)
	return &item, nil
}

func loadAnnouncementStates(userID int, rows []Announcement) (map[uint]*AnnouncementUserState, error) {
	result := map[uint]*AnnouncementUserState{}
	if userID <= 0 || len(rows) == 0 {
		return result, nil
	}
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	var states []AnnouncementUserState
	if err := CoreDB().Where("user_id = ? AND announcement_id IN ?", userID, ids).Find(&states).Error; err != nil {
		return nil, err
	}
	for i := range states {
		state := states[i]
		result[state.AnnouncementID] = &state
	}
	return result, nil
}

func markAnnouncementFirstShown(announcementID uint, userID int, now time.Time) error {
	if CoreDB() == nil || announcementID == 0 || userID <= 0 {
		return nil
	}
	row := AnnouncementUserState{
		AnnouncementID: announcementID,
		UserID:         userID,
		FirstShownAt:   &now,
	}
	return CoreDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "announcement_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"first_shown_at": gorm.Expr("COALESCE(announcement_user_states.first_shown_at, ?)", now),
			"updated_at":     now,
		}),
	}).Create(&row).Error
}

func DismissAnnouncement(announcementID uint, userID int) error {
	if CoreDB() == nil || announcementID == 0 || userID <= 0 {
		return nil
	}
	now := time.Now().UTC()
	row := AnnouncementUserState{
		AnnouncementID: announcementID,
		UserID:         userID,
		FirstShownAt:   &now,
		DismissedAt:    &now,
	}
	return CoreDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "announcement_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"first_shown_at": gorm.Expr("COALESCE(announcement_user_states.first_shown_at, ?)", now),
			"dismissed_at":   now,
			"updated_at":     now,
		}),
	}).Create(&row).Error
}

func ListAdminAnnouncements(limit int, locale string, beforeID uint, audience ...string) ([]AnnouncementView, uint, error) {
	if CoreDB() == nil {
		return nil, 0, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows []Announcement
	query := CoreDB().Order("id DESC")
	if len(audience) > 0 && audience[0] != "" {
		query = announcementAudienceFilter(query, audience[0])
	}
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	if err := query.Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	nextBeforeID := uint(0)
	if len(rows) > limit {
		rows = rows[:limit]
		nextBeforeID = rows[len(rows)-1].ID
	}
	views := make([]AnnouncementView, 0, len(rows))
	for _, row := range rows {
		views = append(views, row.ToView(locale, nil))
	}
	return views, nextBeforeID, nil
}

func announcementAudienceFilter(query *gorm.DB, audience string) *gorm.DB {
	if common.UsingPostgreSQL {
		return query.Where("jsonb_exists(audiences::jsonb, ?)", audience)
	}
	return query.Where("CAST(audiences AS CHAR) LIKE ?", `%"`+audience+`"%`)
}

func announcementConsoleFilter(query *gorm.DB) *gorm.DB {
	if common.UsingPostgreSQL {
		return query.Where("announcements.audiences IS NULL OR jsonb_exists(announcements.audiences::jsonb, ?)", AnnouncementAudienceConsole)
	}
	return query.Where("announcements.audiences IS NULL OR CAST(announcements.audiences AS CHAR) LIKE ?", `%"console"%`)
}

func ListDocsChangelog(locale string, limit int, beforeID uint) ([]AnnouncementView, uint, error) {
	if CoreDB() == nil {
		return []AnnouncementView{}, 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := announcementAudienceFilter(CoreDB().Model(&Announcement{}).Where("published_at IS NOT NULL AND unpublished_at IS NULL"), AnnouncementAudienceDocs)
	if beforeID > 0 {
		var cursor Announcement
		if err := CoreDB().Select("id", "published_at").First(&cursor, beforeID).Error; err != nil {
			return nil, 0, err
		}
		query = query.Where("published_at < ? OR (published_at = ? AND id < ?)", cursor.PublishedAt, cursor.PublishedAt, beforeID)
	}
	var rows []Announcement
	if err := query.Order("published_at DESC").Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	nextBefore := uint(0)
	if len(rows) > limit {
		rows = rows[:limit]
		nextBefore = rows[len(rows)-1].ID
	}
	views := make([]AnnouncementView, 0, len(rows))
	for _, row := range rows {
		views = append(views, row.ToView(locale, nil))
	}
	return views, nextBefore, nil
}

func UnpublishAnnouncement(id uint) error {
	if CoreDB() == nil || id == 0 {
		return nil
	}
	now := time.Now().UTC()
	return CoreDB().Model(&Announcement{}).Where("id = ?", id).Updates(map[string]any{
		"unpublished_at": now,
		"updated_at":     now,
	}).Error
}

func MigrateLegacyAnnouncements(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	var existing int64
	if err := db.Model(&Announcement{}).Where("source = ?", AnnouncementSourceLegacySetting).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	raw := legacyAnnouncementOptionValue(db)
	rows, err := legacyAnnouncementRowsFromJSON(raw, time.Now().UTC())
	if err != nil {
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	return db.Create(&rows).Error
}

func BackfillAnnouncementPublicationFields(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if err := db.Model(&Announcement{}).Where("audiences IS NULL").Update("audiences", JSONValue(`["console"]`)).Error; err != nil {
		return err
	}
	return db.Exec(`UPDATE announcements SET version = (SELECT version FROM release_deployments WHERE release_deployments.id = announcements.source_deployment_id) WHERE source = ? AND source_deployment_id IS NOT NULL AND (version IS NULL OR version = '')`, AnnouncementSourceDeploy).Error
}

func SyncLegacyAnnouncements(raw string) error {
	if CoreDB() == nil {
		return nil
	}
	now := time.Now().UTC()
	rows, err := legacyAnnouncementRowsFromJSON(raw, now)
	if err != nil {
		return err
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var existing []Announcement
		if err := tx.
			Where("source = ?", AnnouncementSourceLegacySetting).
			Order("published_at DESC").
			Order("id DESC").
			Find(&existing).Error; err != nil {
			return err
		}
		for index := range rows {
			if index >= len(existing) {
				if err := tx.Create(&rows[index]).Error; err != nil {
					return err
				}
				continue
			}
			row := rows[index]
			if err := tx.Model(&Announcement{}).Where("id = ?", existing[index].ID).Updates(map[string]any{
				"kind":           row.Kind,
				"title_i18n":     row.TitleI18N,
				"summary_i18n":   row.SummaryI18N,
				"body_i18n":      row.BodyI18N,
				"default_locale": row.DefaultLocale,
				"source":         row.Source,
				"published_at":   row.PublishedAt,
				"unpublished_at": nil,
				"updated_at":     now,
			}).Error; err != nil {
				return err
			}
		}
		if len(existing) > len(rows) {
			ids := make([]uint, 0, len(existing)-len(rows))
			for _, row := range existing[len(rows):] {
				ids = append(ids, row.ID)
			}
			if err := tx.Where("id IN ?", ids).Delete(&Announcement{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func legacyAnnouncementRowsFromJSON(raw string, now time.Time) ([]Announcement, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	rows := make([]Announcement, 0, len(list))
	for _, item := range list {
		title := strings.TrimSpace(toString(item["title"]))
		content := strings.TrimSpace(toString(item["content"]))
		if title == "" && content == "" {
			continue
		}
		publishedAt := parseLegacyAnnouncementTime(item["publishDate"], now)
		if !publishedAt.IsZero() && publishedAt.Before(now.AddDate(0, 0, -legacyAnnouncementRetentionDays)) {
			continue
		}
		rows = append(rows, Announcement{
			Kind:          normalizeAnnouncementKind(toString(item["type"])),
			TitleI18N:     marshalJSONValue(map[string]string{"zh": title, "en": title}, "{}"),
			SummaryI18N:   marshalJSONValue(map[string]string{"zh": content, "en": content}, "{}"),
			BodyI18N:      marshalJSONValue(map[string]any{"zh": []string{content}, "en": []string{content}}, "{}"),
			DefaultLocale: "zh",
			Source:        AnnouncementSourceLegacySetting,
			PublishedAt:   &publishedAt,
		})
	}
	return rows, nil
}

func legacyAnnouncementOptionValue(db *gorm.DB) string {
	for _, key := range []string{"console_setting.announcements", "Announcements"} {
		var option Option
		if err := db.Where("key = ?", key).First(&option).Error; err == nil {
			return option.Value
		}
	}
	return ""
}

func parseLegacyAnnouncementTime(value any, fallback time.Time) time.Time {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return fallback
		}
		if parsed, err := time.Parse(time.RFC3339, typed); err == nil {
			return parsed.UTC()
		}
		if parsed, err := time.Parse("2006-01-02 15:04:05", typed); err == nil {
			return parsed.UTC()
		}
	case float64:
		if typed > 0 {
			return time.Unix(int64(typed), 0).UTC()
		}
	case int64:
		if typed > 0 {
			return time.Unix(typed, 0).UTC()
		}
	case int:
		if typed > 0 {
			return time.Unix(int64(typed), 0).UTC()
		}
	}
	return fallback
}

func LatestReleaseDeployment() (*ReleaseDeployment, error) {
	if CoreDB() == nil {
		return nil, nil
	}
	var row ReleaseDeployment
	err := CoreDB().Order("deployed_at DESC").Order("id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ReleaseDeploymentForCommit returns the newest recorded live transition for a
// commit. It lets a re-versioned deployment retain its original range instead
// of turning an idempotent retry into current→current.
func ReleaseDeploymentForCommit(commitSHA string) (*ReleaseDeployment, error) {
	if CoreDB() == nil || strings.TrimSpace(commitSHA) == "" {
		return nil, nil
	}
	var row ReleaseDeployment
	err := CoreDB().Where("commit_sha = ?", strings.TrimSpace(commitSHA)).
		Order("deployed_at DESC").
		Order("id DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func LatestGeneratedReleaseDeployment() (*ReleaseDeployment, error) {
	if CoreDB() == nil {
		return nil, nil
	}
	var row ReleaseDeployment
	err := CoreDB().
		Joins("JOIN announcements ON announcements.id = release_deployments.release_note_announcement_id").
		Where("release_deployments.release_note_status = ? AND release_deployments.release_note_announcement_id IS NOT NULL", ReleaseNoteStatusGenerated).
		Where("announcements.published_at IS NOT NULL AND announcements.unpublished_at IS NULL").
		Order("release_deployments.deployed_at DESC").
		Order("release_deployments.id DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// GeneratedReleaseDeploymentForRange finds the already-generated draft for a
// deployment range. Retries use it before calling the release-note model so a
// delivery-level correction is not made contingent on a second model call.
func GeneratedReleaseDeploymentForRange(previousCommitSHA, commitSHA string) (*ReleaseDeployment, *Announcement, error) {
	if CoreDB() == nil {
		return nil, nil, nil
	}
	var deployment ReleaseDeployment
	err := CoreDB().
		Where("previous_commit_sha = ? AND commit_sha = ?", previousCommitSHA, commitSHA).
		Where("release_note_status = ? AND release_note_announcement_id IS NOT NULL", ReleaseNoteStatusGenerated).
		First(&deployment).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var announcement Announcement
	if err := CoreDB().First(&announcement, *deployment.ReleaseNoteAnnouncementID).Error; err != nil {
		return nil, nil, err
	}
	return &deployment, &announcement, nil
}

func UpdateGeneratedAnnouncementDelivery(announcementID uint, delivery string) (*Announcement, error) {
	if CoreDB() == nil || announcementID == 0 {
		return nil, errors.New("announcement is required")
	}
	normalizedDelivery := normalizeAnnouncementDelivery(delivery)
	if err := CoreDB().Model(&Announcement{}).Where("id = ?", announcementID).Updates(map[string]any{
		"delivery":   normalizedDelivery,
		"updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return nil, err
	}
	var announcement Announcement
	if err := CoreDB().First(&announcement, announcementID).Error; err != nil {
		return nil, err
	}
	return &announcement, nil
}

func RecordReleaseDeploymentWithAnnouncement(deployment *ReleaseDeployment, announcementInput *AnnouncementUpsert) (*ReleaseDeployment, *Announcement, error) {
	if CoreDB() == nil {
		return nil, nil, errors.New("database is not initialized")
	}
	if deployment == nil {
		return nil, nil, errors.New("deployment is required")
	}
	if deployment.DeployedAt.IsZero() {
		deployment.DeployedAt = time.Now().UTC()
	}
	if deployment.ReleaseNoteStatus == "" {
		deployment.ReleaseNoteStatus = ReleaseNoteStatusSkipped
	}
	var savedAnnouncement *Announcement
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var existing ReleaseDeployment
		err := tx.
			Where("previous_commit_sha = ? AND commit_sha = ?", deployment.PreviousCommitSHA, deployment.CommitSHA).
			First(&existing).Error
		// Silent retries do not carry a release-note range. Match the existing
		// commit record before inferring a new previous SHA, otherwise a retry of
		// the current deployment would create a bogus current→current row.
		if errors.Is(err, gorm.ErrRecordNotFound) && announcementInput == nil {
			err = tx.Where("commit_sha = ?", deployment.CommitSHA).
				Order("deployed_at DESC").
				Order("id DESC").
				First(&existing).Error
		}
		if err == nil {
			if existing.ReleaseNoteStatus == ReleaseNoteStatusGenerated && existing.ReleaseNoteAnnouncementID != nil {
				var announcement Announcement
				if err := tx.First(&announcement, *existing.ReleaseNoteAnnouncementID).Error; err != nil {
					return err
				}
				if announcementInput != nil && announcementInput.DeliverySet {
					if announcement.PublishedAt != nil && announcement.UnpublishedAt == nil {
						return errors.New("generated release announcement is already published; create a new reviewed announcement instead")
					}
					delivery := normalizeAnnouncementDelivery(announcementInput.Delivery)
					if announcement.Delivery != delivery {
						if err := tx.Model(&announcement).Updates(map[string]any{
							"delivery":   delivery,
							"updated_at": time.Now().UTC(),
						}).Error; err != nil {
							return err
						}
						announcement.Delivery = delivery
					}
				}
				updates := map[string]any{
					"version":     deployment.Version,
					"deployed_at": deployment.DeployedAt,
					"updated_at":  time.Now().UTC(),
				}
				if err := tx.Model(&ReleaseDeployment{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
					return err
				}
				if err := tx.Model(&Announcement{}).Where("id = ?", announcement.ID).Update("version", deployment.Version).Error; err != nil {
					return err
				}
				announcement.Version = deployment.Version
				savedAnnouncement = &announcement
				return tx.First(deployment, existing.ID).Error
			}

			updates := map[string]any{
				"version":             deployment.Version,
				"deployed_at":         deployment.DeployedAt,
				"release_note_status": deployment.ReleaseNoteStatus,
				"release_note_error":  deployment.ReleaseNoteError,
				"updated_at":          time.Now().UTC(),
			}
			if announcementInput != nil {
				announcementInput.SourceDeploymentID = &existing.ID
				announcementInput.Version = deployment.Version
				announcement, err := upsertAnnouncementWithDB(tx, *announcementInput)
				if err != nil {
					return err
				}
				savedAnnouncement = announcement
				updates["release_note_announcement_id"] = announcement.ID
				updates["release_note_status"] = ReleaseNoteStatusGenerated
				updates["release_note_error"] = ""
			}
			if err := tx.Model(&ReleaseDeployment{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
				return err
			}
			return tx.First(deployment, existing.ID).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if err := tx.Create(deployment).Error; err != nil {
			return err
		}
		if announcementInput != nil {
			announcementInput.SourceDeploymentID = &deployment.ID
			announcementInput.Version = deployment.Version
			announcement, err := upsertAnnouncementWithDB(tx, *announcementInput)
			if err != nil {
				return err
			}
			savedAnnouncement = announcement
			if err := tx.Model(deployment).Updates(map[string]any{
				"release_note_announcement_id": announcement.ID,
				"release_note_status":          ReleaseNoteStatusGenerated,
				"release_note_error":           "",
				"updated_at":                   time.Now().UTC(),
			}).Error; err != nil {
				return err
			}
			deployment.ReleaseNoteAnnouncementID = &announcement.ID
			deployment.ReleaseNoteStatus = ReleaseNoteStatusGenerated
			deployment.ReleaseNoteError = ""
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return deployment, savedAnnouncement, nil
}
