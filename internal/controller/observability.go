package controller

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	workerservice "github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/setting/console_setting"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

const (
	publicStatusOperational = "operational"
	publicStatusDegraded    = "degraded"
	publicStatusOutage      = "outage"
	publicStatusUnknown     = "unknown"

	healthAvailabilityDegraded = 0.99
	healthAvailabilityOutage   = 0.95
	healthErrorRateDegraded    = 0.01
	healthLatencyDegradedMs    = 8000
)

type publicStatusResponse struct {
	OverallStatus string                   `json:"overall_status"`
	UpdatedAt     time.Time                `json:"updated_at"`
	Components    []publicStatusComponent  `json:"components"`
	ModelFamilies []publicModelFamily      `json:"model_families"`
	Updates       []publicAnnouncementView `json:"updates"`
}

type publicStatusComponent struct {
	ID          string            `json:"id"`
	NameI18N    map[string]string `json:"name_i18n"`
	Status      string            `json:"status"`
	Description string            `json:"description,omitempty"`
	UpdatedAt   *time.Time        `json:"updated_at,omitempty"`
}

type publicModelFamily struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Availability   *float64 `json:"availability,omitempty"`
	LatencyP95Ms   *int     `json:"latency_p95_ms,omitempty"`
	ModelCount     int      `json:"model_count"`
	AvailableCount int      `json:"available_count"`
}

type publicAnnouncementView struct {
	ID          uint      `json:"id"`
	Version     string    `json:"version,omitempty"`
	Kind        string    `json:"kind"`
	Type        string    `json:"type"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary,omitempty"`
	Content     string    `json:"content"`
	Body        []string  `json:"body,omitempty"`
	PublishDate time.Time `json:"publishDate"`
}

type adminHealthResponse struct {
	Window         string               `json:"window"`
	WindowSeconds  int64                `json:"window_seconds"`
	Models         []adminHealthRow     `json:"models"`
	Channels       []adminHealthRow     `json:"channels"`
	ModelChannels  []adminHealthRow     `json:"model_channels"`
	ErrorBuckets   []adminErrorBucket   `json:"error_buckets"`
	RecentFailures []adminFailureSample `json:"recent_failures"`
	Queues         map[string]any       `json:"queues"`
	Settlement     map[string]any       `json:"settlement"`
	Workers        map[string]any       `json:"workers"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

type publicStatusCacheEntry struct {
	payload publicStatusResponse
	expires time.Time
}

var publicStatusCache = struct {
	sync.Mutex
	items map[string]publicStatusCacheEntry
}{
	items: map[string]publicStatusCacheEntry{},
}

type adminHealthRow struct {
	ModelName     string     `json:"model_name,omitempty"`
	ChannelID     *int       `json:"channel_id,omitempty"`
	ChannelName   string     `json:"channel_name,omitempty"`
	Status        string     `json:"status"`
	RequestCount  int64      `json:"request_count"`
	ErrorCount    int64      `json:"error_count"`
	ErrorRate     float64    `json:"error_rate"`
	Availability  float64    `json:"availability"`
	AvgLatencyMs  float64    `json:"avg_latency_ms"`
	P95LatencyMs  float64    `json:"p95_latency_ms"`
	LastOutcomeAt *time.Time `json:"last_outcome_at,omitempty"`
}

type adminErrorBucket struct {
	Bucket string `json:"bucket"`
	Count  int64  `json:"count"`
}

type adminFailureSample struct {
	RequestID   string    `json:"request_id"`
	ModelName   string    `json:"model_name"`
	ChannelID   *int      `json:"channel_id,omitempty"`
	ChannelName string    `json:"channel_name,omitempty"`
	StatusCode  *int      `json:"status_code,omitempty"`
	ErrorCode   string    `json:"error_code,omitempty"`
	ErrorType   string    `json:"error_type,omitempty"`
	DurationMs  int       `json:"duration_ms"`
	OutcomeAt   time.Time `json:"outcome_at"`
}

type adminHealthAggregateRow struct {
	ModelName     string
	ChannelID     *int
	ChannelName   string
	RequestCount  int64
	ErrorCount    int64
	AvgLatencyMs  float64
	P95LatencyMs  float64
	LastOutcomeAt adminHealthNullableTime
}

type adminHealthNullableTime struct {
	Time  time.Time
	Valid bool
}

func (t *adminHealthNullableTime) Scan(value any) error {
	switch typed := value.(type) {
	case nil:
		t.Valid = false
		return nil
	case time.Time:
		t.Time = typed
		t.Valid = true
		return nil
	case string:
		return t.scanString(typed)
	case []byte:
		return t.scanString(string(typed))
	default:
		return fmt.Errorf("unsupported admin health time value %T", value)
	}
}

func (t adminHealthNullableTime) Value() (driver.Value, error) {
	if !t.Valid {
		return nil, nil
	}
	return t.Time, nil
}

func (t *adminHealthNullableTime) scanString(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		t.Valid = false
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05.999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			t.Time = parsed.UTC()
			t.Valid = true
			return nil
		}
	}
	return fmt.Errorf("parse admin health time %q", value)
}

type adminFailureSQLRow struct {
	RequestID   string
	ModelName   string
	ChannelID   *int
	ChannelName string
	StatusCode  *int
	ErrorCode   string
	ErrorType   string
	DurationMs  int
	OutcomeAt   time.Time
}

func requestLocale(c *gin.Context) string {
	if locale := c.Query("locale"); locale != "" {
		return model.NormalizeAnnouncementLocale(locale)
	}
	if locale := c.GetHeader("X-Lazu-Locale"); locale != "" {
		return model.NormalizeAnnouncementLocale(locale)
	}
	return model.NormalizeAnnouncementLocale(c.GetHeader("Accept-Language"))
}

func GetPublicStatus(c *gin.Context) {
	locale := requestLocale(c)
	announcementsEnabled := console_setting.GetConsoleSetting().AnnouncementsEnabled
	cacheKey := fmt.Sprintf("%s:%t", locale, announcementsEnabled)
	if payload, ok := readPublicStatusCache(cacheKey, false); ok {
		common.ApiSuccess(c, payload)
		return
	}
	components := buildPublicComponents(c.Request.Context())
	families := buildPublicModelFamilies()
	updates := []publicAnnouncementView{}
	if announcementsEnabled {
		items, err := model.ListPublishedAnnouncements(5, locale, 0)
		if err != nil {
			if payload, ok := readPublicStatusCache(cacheKey, true); ok {
				common.ApiSuccess(c, payload)
				return
			}
			httperr.Write(c, err)
			return
		}
		updates = publicAnnouncementViews(items)
	}
	payload := publicStatusResponse{
		OverallStatus: overallPublicStatus(components, families),
		UpdatedAt:     time.Now().UTC(),
		Components:    components,
		ModelFamilies: families,
		Updates:       updates,
	}
	writePublicStatusCache(cacheKey, payload)
	common.ApiSuccess(c, payload)
}

// GetAdminHealth reports platform-wide reliability: model, channel and
// model-by-channel aggregates plus queue, settlement and worker state. The
// per-lane view users also see lives in GetChannelHealth; this one is the
// operator's whole-system picture.
func GetAdminHealth(c *gin.Context) {
	windowLabel, since := parseHealthWindow(c.Query("window"))
	modelRows, err := queryAdminHealthRows(since, "model")
	if err != nil {
		httperr.Write(c, err)
		return
	}
	channelRows, err := queryAdminHealthRows(since, "channel")
	if err != nil {
		httperr.Write(c, err)
		return
	}
	modelChannelRows, err := queryAdminHealthRows(since, "model_channel")
	if err != nil {
		httperr.Write(c, err)
		return
	}
	errorBuckets, err := queryAdminErrorBuckets(since)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	recentFailures, err := queryAdminRecentFailures(since)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, adminHealthResponse{
		Window:         windowLabel,
		WindowSeconds:  int64(time.Since(since).Seconds()),
		Models:         modelRows,
		Channels:       channelRows,
		ModelChannels:  modelChannelRows,
		ErrorBuckets:   errorBuckets,
		RecentFailures: recentFailures,
		Queues:         relayQueueSummary(c.Request.Context()),
		Settlement:     settlementSummary(),
		Workers:        workerSummary(),
		UpdatedAt:      time.Now().UTC(),
	})
}

func readPublicStatusCache(locale string, allowStale bool) (publicStatusResponse, bool) {
	publicStatusCache.Lock()
	defer publicStatusCache.Unlock()
	entry, ok := publicStatusCache.items[locale]
	if !ok || (!allowStale && time.Now().UTC().After(entry.expires)) {
		return publicStatusResponse{}, false
	}
	return entry.payload, true
}

func writePublicStatusCache(locale string, payload publicStatusResponse) {
	publicStatusCache.Lock()
	defer publicStatusCache.Unlock()
	publicStatusCache.items[locale] = publicStatusCacheEntry{
		payload: payload,
		expires: time.Now().UTC().Add(45 * time.Second),
	}
}

func ListAnnouncements(c *gin.Context) {
	if !console_setting.GetConsoleSetting().AnnouncementsEnabled {
		common.ApiSuccess(c, gin.H{"items": []model.AnnouncementView{}})
		return
	}
	limit := parseLimit(c.Query("limit"), 50)
	items, err := model.ListPublishedAnnouncements(limit, requestLocale(c), c.GetInt("id"))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

func ListUnseenAnnouncements(c *gin.Context) {
	if !console_setting.GetConsoleSetting().AnnouncementsEnabled {
		common.ApiSuccess(c, gin.H{"items": []model.AnnouncementView{}})
		return
	}
	items, err := model.ListUnseenAnnouncements(c.GetInt("id"), requestLocale(c), parseLimit(c.Query("limit"), 20))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	criticalItem, err := model.UnseenCriticalAnnouncement(c.GetInt("id"), requestLocale(c))
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"critical_item": criticalItem,
		"has_critical":  criticalItem != nil,
		"items":         items,
	})
}

func publicAnnouncementViews(items []model.AnnouncementView) []publicAnnouncementView {
	result := make([]publicAnnouncementView, 0, len(items))
	for _, item := range items {
		result = append(result, publicAnnouncementView{
			ID:          item.ID,
			Version:     item.Version,
			Kind:        item.Kind,
			Type:        item.Type,
			Title:       item.Title,
			Summary:     item.Summary,
			Content:     item.Content,
			Body:        item.Body,
			PublishDate: item.PublishDate,
		})
	}
	return result
}

func DismissAnnouncement(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httperr.Invalid(c, "id")
		return
	}
	if err := model.DismissAnnouncement(uint(id), c.GetInt("id")); err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"id": id})
}

func AdminListAnnouncements(c *gin.Context) {
	audience := strings.TrimSpace(c.Query("audience"))
	if audience != "" && audience != model.AnnouncementAudienceDocs && audience != model.AnnouncementAudienceConsole {
		httperr.Invalid(c, "audience")
		return
	}
	beforeID, err := strconv.ParseUint(c.Query("before_id"), 10, 64)
	if err != nil && strings.TrimSpace(c.Query("before_id")) != "" {
		httperr.Invalid(c, "before_id")
		return
	}
	items, nextBeforeID, err := model.ListAdminAnnouncements(
		parseLimit(c.Query("limit"), 100),
		requestLocale(c),
		uint(beforeID),
		audience,
	)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "next_before_id": nextBeforeID})
}

type announcementPayload struct {
	Kind          string            `json:"kind"`
	TitleI18N     map[string]string `json:"title_i18n"`
	SummaryI18N   map[string]string `json:"summary_i18n"`
	BodyI18N      map[string]any    `json:"body_i18n"`
	DefaultLocale string            `json:"default_locale"`
	// Model-update announcements carry the models they concern and the day the
	// change lands. Both are pointers so an edit that omits them leaves the
	// stored values alone instead of clearing them.
	AffectedModels *[]string          `json:"affected_models"`
	EffectiveDate  *string            `json:"effective_date"`
	Published      *bool              `json:"published"`
	Audiences      *[]string          `json:"audiences"`
	APIChanges     *[]model.APIChange `json:"api_changes"`
	PublishedAt    *string            `json:"published_at"`
}

// The editor sends a plain calendar day, while the admin list returns RFC3339.
// Accept both so a read-modify-write API client can round-trip the response.
func parseAnnouncementEffectiveDate(raw *string) (*time.Time, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, true, nil
	}
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, trimmed)
	}
	if err != nil {
		return nil, false, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "effective_date", "reason": "effective_date must be YYYY-MM-DD or RFC3339"})
	}
	return &parsed, true, nil
}

func AdminCreateAnnouncement(c *gin.Context) {
	saveAdminAnnouncement(c, 0)
}

func AdminUpdateAnnouncement(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httperr.Invalid(c, "id")
		return
	}
	saveAdminAnnouncement(c, uint(id))
}

func saveAdminAnnouncement(c *gin.Context, id uint) {
	var req announcementPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest, types.D{"reason": "the body is not a valid announcement"})
		return
	}
	published := id == 0
	if req.Published != nil {
		published = *req.Published
	}
	if req.Audiences != nil {
		for _, audience := range *req.Audiences {
			if audience != model.AnnouncementAudienceConsole && audience != model.AnnouncementAudienceDocs {
				httperr.Invalid(c, "audiences")
				return
			}
		}
	}
	if req.APIChanges != nil {
		validated := workerservice.ValidateAPIChanges(*req.APIChanges)
		if len(validated) != len(*req.APIChanges) {
			httperr.Invalid(c, "api_changes")
			return
		}
		req.APIChanges = &validated
	}
	var publishedAt *time.Time
	if req.PublishedAt != nil {
		if id == 0 {
			httperr.Invalid(c, "published_at", types.D{"reason": "published_at can only be backfilled on an existing announcement"})
			return
		}
		if c.GetInt("role") < common.RoleRootUser {
			httperr.Code(c, types.ErrorCodeAccessDenied)
			return
		}
		parsed, err := time.Parse(time.RFC3339, *req.PublishedAt)
		if err != nil {
			httperr.Invalid(c, "published_at", types.D{"reason": "published_at must be RFC3339"})
			return
		}
		publishedAt = &parsed
	}
	effectiveDate, effectiveDateSet, err := parseAnnouncementEffectiveDate(req.EffectiveDate)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	affectedModels := []string{}
	if req.AffectedModels != nil {
		affectedModels = *req.AffectedModels
	}
	row, err := model.UpsertAnnouncement(model.AnnouncementUpsert{
		ID:                id,
		AffectedModels:    affectedModels,
		AffectedModelsSet: req.AffectedModels != nil,
		Audiences:         req.Audiences,
		APIChanges:        req.APIChanges,
		PublishedAt:       publishedAt,
		PublishedAtSet:    req.PublishedAt != nil,
		EffectiveDate:     effectiveDate,
		EffectiveDateSet:  effectiveDateSet,
		Kind:              req.Kind,
		TitleI18N:         req.TitleI18N,
		SummaryI18N:       req.SummaryI18N,
		BodyI18N:          req.BodyI18N,
		DefaultLocale:     req.DefaultLocale,
		Source:            model.AnnouncementSourceManual,
		Published:         published,
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		httperr.Code(c, types.ErrorCodeAnnouncementNotFound, types.D{"announcement_id": id})
		return
	}
	if err != nil {
		httperr.Write(c, err)
		return
	}
	invalidateChangelogCache()
	common.ApiSuccess(c, row.ToView(requestLocale(c), nil))
}

func AdminUnpublishAnnouncement(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httperr.Invalid(c, "id")
		return
	}
	if err := model.UnpublishAnnouncement(uint(id)); err != nil {
		httperr.Write(c, err)
		return
	}
	invalidateChangelogCache()
	common.ApiSuccess(c, gin.H{"id": id})
}

func buildPublicComponents(ctx context.Context) []publicStatusComponent {
	now := time.Now().UTC()
	probes := FetchUptimeKumaGroups(ctx)
	components := []publicStatusComponent{
		{
			ID:        "api",
			NameI18N:  map[string]string{"zh": "API 网关", "en": "API Gateway", "zh-TW": "API 閘道", "ja": "API Gateway"},
			Status:    publicStatusOperational,
			UpdatedAt: &now,
		},
		{
			ID:        "console",
			NameI18N:  map[string]string{"zh": "控制台", "en": "Console", "zh-TW": "控制台", "ja": "コンソール"},
			Status:    componentProbeStatus(probes, []string{"console", "frontend", "web", "控制台"}),
			UpdatedAt: &now,
		},
		{
			ID:        "docs",
			NameI18N:  map[string]string{"zh": "文档", "en": "Docs", "zh-TW": "文件", "ja": "ドキュメント"},
			Status:    componentProbeStatus(probes, []string{"docs", "documentation", "文档"}),
			UpdatedAt: &now,
		},
		{
			ID:        "billing",
			NameI18N:  map[string]string{"zh": "计费与用量", "en": "Billing and usage", "zh-TW": "計費與用量", "ja": "請求と使用量"},
			Status:    billingComponentStatus(),
			UpdatedAt: &now,
		},
	}
	if status := componentProbeStatus(probes, []string{"api", "gateway", "backend"}); status != publicStatusUnknown {
		components[0].Status = status
	}
	return components
}

func componentProbeStatus(groups []UptimeGroupResult, names []string) string {
	if len(groups) == 0 {
		return publicStatusUnknown
	}
	matched := false
	for _, group := range groups {
		for _, monitor := range group.Monitors {
			monitorName := strings.ToLower(monitor.Name + " " + monitor.Group + " " + group.CategoryName)
			for _, name := range names {
				if strings.Contains(monitorName, strings.ToLower(name)) {
					matched = true
					if monitor.Status != 1 {
						return publicStatusOutage
					}
				}
			}
		}
	}
	if matched {
		return publicStatusOperational
	}
	return publicStatusUnknown
}

func billingComponentStatus() string {
	if model.CoreDB() == nil || !model.CoreDB().Migrator().HasTable(&model.SettlementFailure{}) {
		return publicStatusUnknown
	}
	ids, err := model.VerifiedUnresolvedSettlementFailureIDs(context.Background(), model.CoreDB())
	count := int64(len(ids))
	if err != nil {
		return publicStatusUnknown
	}
	if count > 0 {
		return publicStatusDegraded
	}
	return publicStatusOperational
}

func buildPublicModelFamilies() []publicModelFamily {
	modelNames := model.GetEnabledModels()
	if len(modelNames) == 0 {
		return []publicModelFamily{}
	}
	healthByModel := loadCatalogHealthByModel(modelNames)
	type familyAccumulator struct {
		item             publicModelFamily
		availabilitySum  float64
		availabilitySeen int
	}
	acc := map[string]*familyAccumulator{}
	for _, modelName := range modelNames {
		familyID, familyName := inferModelFamily(modelName)
		if _, ok := acc[familyID]; !ok {
			acc[familyID] = &familyAccumulator{
				item: publicModelFamily{
					ID:     familyID,
					Name:   familyName,
					Status: publicStatusOperational,
				},
			}
		}
		entry := acc[familyID]
		entry.item.ModelCount++
		health := healthByModel[modelName]
		entry.item.Status = worsePublicStatus(entry.item.Status, catalogHealthStatusToPublic(health.Status))
		if health.Status == "available" || health.Status == "degraded" {
			entry.item.AvailableCount++
		}
		if health.Availability != nil {
			entry.availabilitySum += *health.Availability
			entry.availabilitySeen++
		}
		if health.LatencyP95Ms != nil {
			if entry.item.LatencyP95Ms == nil || *health.LatencyP95Ms > *entry.item.LatencyP95Ms {
				value := *health.LatencyP95Ms
				entry.item.LatencyP95Ms = &value
			}
		}
	}
	families := make([]publicModelFamily, 0, len(acc))
	for _, entry := range acc {
		if entry.availabilitySeen > 0 {
			value := entry.availabilitySum / float64(entry.availabilitySeen)
			entry.item.Availability = &value
		}
		families = append(families, entry.item)
	}
	sort.Slice(families, func(i, j int) bool {
		return families[i].Name < families[j].Name
	})
	return families
}

func inferModelFamily(modelName string) (string, string) {
	lower := strings.ToLower(modelName)
	switch {
	case strings.Contains(lower, "deepseek"):
		return "deepseek", "DeepSeek"
	case strings.Contains(lower, "claude"):
		return "claude", "Claude"
	case strings.Contains(lower, "gemini"):
		return "gemini", "Gemini"
	case strings.Contains(lower, "qwen"):
		return "qwen", "Qwen"
	case strings.Contains(lower, "gpt") || strings.Contains(lower, "o1") || strings.Contains(lower, "o3") || strings.Contains(lower, "o4"):
		return "openai", "OpenAI"
	case strings.Contains(lower, "grok"):
		return "xai", "xAI"
	case strings.Contains(lower, "moonshot") || strings.Contains(lower, "kimi"):
		return "moonshot", "Moonshot"
	case strings.Contains(lower, "doubao"):
		return "doubao", "Doubao"
	default:
		return "other", "Other"
	}
}

func catalogHealthStatusToPublic(status string) string {
	switch status {
	case "available":
		return publicStatusOperational
	case "degraded":
		return publicStatusDegraded
	case "unavailable":
		return publicStatusOutage
	default:
		return publicStatusUnknown
	}
}

func parseHealthWindow(raw string) (string, time.Time) {
	now := time.Now().UTC()
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "5m":
		return "5m", now.Add(-5 * time.Minute)
	case "1h":
		return "1h", now.Add(-time.Hour)
	case "24h":
		return "24h", now.Add(-24 * time.Hour)
	default:
		return "15m", now.Add(-15 * time.Minute)
	}
}

func queryAdminHealthRows(since time.Time, scope string) ([]adminHealthRow, error) {
	if model.TelemetryDB() == nil || !model.TelemetryDB().Migrator().HasTable(&model.RoutingOutcome{}) {
		return []adminHealthRow{}, nil
	}
	selectDims := "model_name, NULL AS channel_id, '' AS channel_name"
	scopedDims := "COALESCE(NULLIF(d.resolved_model, ''), NULLIF(d.requested_model, ''), 'unknown') AS model_name"
	partitionBy := "model_name"
	groupBy := "model_name"
	orderTie := "model_name ASC"
	joinSQL := "LEFT JOIN routing_decision AS d ON d.id = o.decision_id"
	requiresDecision := true

	switch scope {
	case "channel":
		requiresDecision = false
		selectDims = "'' AS model_name, channel_id, channel_name"
		scopedDims = "o.channel_id AS channel_id, " + adminHealthChannelNameSQL()
		partitionBy = "channel_id, channel_name"
		groupBy = "channel_id, channel_name"
		orderTie = "channel_name ASC"
		joinSQL = adminHealthChannelJoinSQL()
	case "model_channel":
		selectDims = "model_name, channel_id, channel_name"
		scopedDims = "COALESCE(NULLIF(d.resolved_model, ''), NULLIF(d.requested_model, ''), 'unknown') AS model_name, o.channel_id AS channel_id, " + adminHealthChannelNameSQL()
		partitionBy = "model_name, channel_id, channel_name"
		groupBy = "model_name, channel_id, channel_name"
		orderTie = "model_name ASC, channel_name ASC"
		joinSQL = "LEFT JOIN routing_decision AS d ON d.id = o.decision_id " + adminHealthChannelJoinSQL()
	}
	if requiresDecision && !model.TelemetryDB().Migrator().HasTable(&model.RoutingDecision{}) {
		return []adminHealthRow{}, nil
	}

	query := fmt.Sprintf(`
WITH scoped AS (
	SELECT
		%s,
		%s AS is_error,
		CASE
			WHEN COALESCE(o.total_duration_ms, 0) > 0 THEN o.total_duration_ms
			ELSE 0
		END AS duration_ms,
		o.outcome_at AS outcome_at
	FROM %s
	%s
	WHERE o.outcome_at >= ? AND %s
),
ranked AS (
	SELECT
		scoped.*,
		ROW_NUMBER() OVER (PARTITION BY %s ORDER BY duration_ms ASC) AS duration_rank,
		COUNT(*) OVER (PARTITION BY %s) AS group_count
	FROM scoped
)
SELECT
	%s,
	COUNT(*) AS request_count,
	COALESCE(SUM(is_error), 0) AS error_count,
	COALESCE(AVG(duration_ms), 0) AS avg_latency_ms,
	COALESCE(MIN(CASE WHEN duration_rank >= group_count * 0.95 THEN duration_ms END), 0) AS p95_latency_ms,
	MAX(outcome_at) AS last_outcome_at
FROM ranked
GROUP BY %s
ORDER BY request_count DESC, %s
	LIMIT 100`, scopedDims, model.ChannelHealthFailureExpr, model.ChannelHealthOutcomeSourceSQL(), joinSQL, model.ChannelHealthEvidenceExpr,
		partitionBy, partitionBy, selectDims, groupBy, orderTie)

	var rows []adminHealthAggregateRow
	query = strings.ReplaceAll(query, "LEFT JOIN channels ON", "LEFT JOIN "+model.TelemetryChannelTable()+" AS channels ON")
	if err := model.TelemetryDB().Raw(query, since).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]adminHealthRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, adminHealthRowFromAggregate(row))
	}
	return result, nil
}

func adminHealthChannelNameSQL() string {
	if model.TelemetryDB() != nil && model.TelemetryDB().Migrator().HasTable(model.TelemetryChannelTable()) {
		return "COALESCE(NULLIF(channels.name, ''), 'unknown') AS channel_name"
	}
	return "'unknown' AS channel_name"
}

func adminHealthChannelJoinSQL() string {
	if model.TelemetryDB() != nil && model.TelemetryDB().Migrator().HasTable(model.TelemetryChannelTable()) {
		return "LEFT JOIN " + model.TelemetryChannelTable() + " AS channels ON channels.id = o.channel_id"
	}
	return ""
}

func queryAdminErrorBuckets(since time.Time) ([]adminErrorBucket, error) {
	if model.TelemetryDB() == nil || !model.TelemetryDB().Migrator().HasTable(&model.RoutingOutcome{}) {
		return []adminErrorBucket{}, nil
	}
	likeTimeout := "(LOWER(COALESCE(error_code, '')) LIKE ? OR LOWER(COALESCE(error_type, '')) LIKE ?)"
	likeUpstream := "(LOWER(COALESCE(error_code, '')) LIKE ? OR LOWER(COALESCE(error_type, '')) LIKE ?)"
	notTimeoutArgs := []any{"%timeout%", "%timeout%"}
	upstreamArgs := []any{"%upstream%", "%upstream%"}
	specs := []struct {
		bucket string
		where  string
		args   []any
	}{
		{bucket: "rate_limited", where: "COALESCE(status_code, 0) = 429"},
		{bucket: "timeout", where: "COALESCE(status_code, 0) <> 429 AND " + likeTimeout, args: notTimeoutArgs},
		{bucket: "upstream_5xx", where: "COALESCE(status_code, 0) >= 500 AND NOT " + likeTimeout, args: notTimeoutArgs},
		{bucket: "upstream_error", where: "COALESCE(status_code, 0) < 500 AND COALESCE(status_code, 0) <> 429 AND NOT " + likeTimeout + " AND " + likeUpstream, args: append(append([]any{}, notTimeoutArgs...), upstreamArgs...)},
		{bucket: "client_error", where: "COALESCE(status_code, 0) >= 400 AND COALESCE(status_code, 0) < 500 AND COALESCE(status_code, 0) <> 429 AND NOT " + likeTimeout + " AND NOT " + likeUpstream, args: append(append([]any{}, notTimeoutArgs...), upstreamArgs...)},
		{bucket: "other_error", where: "COALESCE(status_code, 0) < 400 AND NOT " + likeTimeout + " AND NOT " + likeUpstream + " AND (COALESCE(error_code, '') <> '' OR COALESCE(error_type, '') <> '')", args: append(append([]any{}, notTimeoutArgs...), upstreamArgs...)},
	}
	result := make([]adminErrorBucket, 0, len(specs))
	for _, spec := range specs {
		var count int64
		args := append([]any{since}, spec.args...)
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE outcome_at >= ? AND %s",
			model.ChannelHealthOutcomeSourceSQL(), spec.where)
		if err := model.TelemetryDB().Raw(query, args...).Scan(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			result = append(result, adminErrorBucket{Bucket: spec.bucket, Count: count})
		}
	}
	return result, nil
}

func queryAdminRecentFailures(since time.Time) ([]adminFailureSample, error) {
	if model.TelemetryDB() == nil || !model.TelemetryDB().Migrator().HasTable(&model.RoutingOutcome{}) || !model.TelemetryDB().Migrator().HasTable(&model.RoutingDecision{}) {
		return []adminFailureSample{}, nil
	}
	var rows []adminFailureSQLRow
	query := fmt.Sprintf(`
SELECT o.request_id AS request_id,
	COALESCE(NULLIF(d.resolved_model, ''), NULLIF(d.requested_model, ''), 'unknown') AS model_name,
	o.channel_id AS channel_id, COALESCE(NULLIF(channels.name, ''), 'unknown') AS channel_name,
	o.status_code AS status_code, o.error_code AS error_code, o.error_type AS error_type,
	COALESCE(o.total_duration_ms, 0) AS duration_ms, o.outcome_at AS outcome_at
FROM %s
LEFT JOIN routing_decision AS d ON d.id = o.decision_id
LEFT JOIN channels ON channels.id = o.channel_id
WHERE o.outcome_at >= ? AND (
	COALESCE(o.status_code, 0) >= 400 OR
	COALESCE(o.error_code, '') <> '' OR
	COALESCE(o.error_type, '') <> ''
)
ORDER BY o.outcome_at DESC
LIMIT 20`, model.ChannelHealthOutcomeSourceSQL())
	query = strings.ReplaceAll(query, "LEFT JOIN channels ON", "LEFT JOIN "+model.TelemetryChannelTable()+" AS channels ON")
	if err := model.TelemetryDB().Raw(query, since).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]adminFailureSample, 0, len(rows))
	for _, row := range rows {
		result = append(result, adminFailureSample{
			RequestID:   row.RequestID,
			ModelName:   row.ModelName,
			ChannelID:   row.ChannelID,
			ChannelName: row.ChannelName,
			StatusCode:  row.StatusCode,
			ErrorCode:   row.ErrorCode,
			ErrorType:   row.ErrorType,
			DurationMs:  row.DurationMs,
			OutcomeAt:   row.OutcomeAt,
		})
	}
	return result, nil
}

func adminHealthRowFromAggregate(row adminHealthAggregateRow) adminHealthRow {
	errorRate := 0.0
	availability := 1.0
	if row.RequestCount > 0 {
		errorRate = float64(row.ErrorCount) / float64(row.RequestCount)
		availability = 1 - errorRate
		if availability < 0 {
			availability = 0
		}
	}
	status := publicStatusOperational
	if row.RequestCount == 0 {
		status = publicStatusUnknown
	} else if availability < healthAvailabilityOutage {
		status = publicStatusOutage
	} else if availability < healthAvailabilityDegraded || errorRate >= healthErrorRateDegraded || row.P95LatencyMs > healthLatencyDegradedMs {
		status = publicStatusDegraded
	}
	channelID := row.ChannelID
	if channelID != nil {
		value := *channelID
		channelID = &value
	}
	var lastOutcomeAt *time.Time
	if row.LastOutcomeAt.Valid {
		value := row.LastOutcomeAt.Time
		lastOutcomeAt = &value
	}
	return adminHealthRow{
		ModelName:     row.ModelName,
		ChannelID:     channelID,
		ChannelName:   row.ChannelName,
		Status:        status,
		RequestCount:  row.RequestCount,
		ErrorCount:    row.ErrorCount,
		ErrorRate:     errorRate,
		Availability:  availability,
		AvgLatencyMs:  row.AvgLatencyMs,
		P95LatencyMs:  row.P95LatencyMs,
		LastOutcomeAt: lastOutcomeAt,
	}
}

func relayQueueSummary(ctx context.Context) map[string]any {
	summary := map[string]any{
		"enabled": common.RelayEventQueueEnabled && common.RedisEnabled && common.RDB != nil,
		"stream":  common.RelayEventStream,
		"group":   common.RelayEventGroup,
		"pending": int64(0),
		"length":  int64(0),
	}
	if !common.RedisEnabled || common.RDB == nil {
		summary["status"] = publicStatusUnknown
		return summary
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if length, err := common.RDB.XLen(reqCtx, common.RelayEventStream).Result(); err == nil {
		summary["length"] = length
	}
	if groups, err := common.RDB.XInfoGroups(reqCtx, common.RelayEventStream).Result(); err == nil {
		for _, group := range groups {
			if group.Name == common.RelayEventGroup {
				summary["pending"] = group.Pending
				summary["consumers"] = group.Consumers
				summary["last_delivered_id"] = group.LastDeliveredID
			}
		}
	}
	pending, _ := summary["pending"].(int64)
	if pending > 0 {
		summary["status"] = publicStatusDegraded
	} else {
		summary["status"] = publicStatusOperational
	}
	return summary
}

func settlementSummary() map[string]any {
	result := map[string]any{
		"open_failures": int64(0),
		"status":        publicStatusUnknown,
	}
	if model.CoreDB() == nil || !model.CoreDB().Migrator().HasTable(&model.SettlementFailure{}) {
		return result
	}
	ids, err := model.VerifiedUnresolvedSettlementFailureIDs(context.Background(), model.CoreDB())
	count := int64(len(ids))
	if err != nil {
		// The status stays unknown; the query error belongs in the log, not
		// in the page.
		common.SysError("admin health: count settlement failures: " + err.Error())
		return result
	}
	var waived int64
	if err := model.CoreDB().Model(&model.LedgerLegacySettlementWaiver{}).Count(&waived).Error; err != nil {
		return result
	}
	result["waived_legacy_failures"] = waived
	result["open_failures"] = count
	if count > 0 {
		result["status"] = publicStatusDegraded
	} else {
		result["status"] = publicStatusOperational
	}
	return result
}

func workerSummary() map[string]any {
	statuses := workerservice.GetWorkerStatusManager().GetAllStatuses()
	result := map[string]any{
		"items":  statuses,
		"status": publicStatusOperational,
	}
	if len(statuses) == 0 {
		result["status"] = publicStatusUnknown
		return result
	}
	for _, status := range statuses {
		if status.Status == "failed" {
			result["status"] = publicStatusDegraded
			break
		}
	}
	return result
}

func overallPublicStatus(components []publicStatusComponent, families []publicModelFamily) string {
	result := publicStatusOperational
	for _, component := range components {
		result = worsePublicStatus(result, component.Status)
	}
	if len(families) == 0 {
		result = worsePublicStatus(result, publicStatusUnknown)
	}
	allFamiliesOutage := len(families) > 0
	for _, family := range families {
		result = worsePublicStatus(result, family.Status)
		if family.Status != publicStatusOutage {
			allFamiliesOutage = false
		}
	}
	if allFamiliesOutage {
		return publicStatusOutage
	}
	return result
}

func worsePublicStatus(left string, right string) string {
	if statusRank(right) > statusRank(left) {
		return right
	}
	return left
}

func statusRank(status string) int {
	switch status {
	case publicStatusOutage:
		return 4
	case publicStatusDegraded:
		return 3
	case publicStatusUnknown:
		return 2
	case publicStatusOperational:
		return 1
	default:
		return 0
	}
}

func parseLimit(raw string, fallback int) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 200 {
		return 200
	}
	return limit
}
