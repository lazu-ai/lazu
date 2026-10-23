package model

import (
	"context"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

type ProjectLogAccess struct {
	Project Project
	Viewer  ProjectMember
	ActorID *int
	TokenID int
}

func NewProjectLogAccess(project Project, viewer ProjectMember) (ProjectLogAccess, error) {
	access := ProjectLogAccess{Project: project, Viewer: viewer}
	if viewer.ProjectID != project.ID || viewer.Status != ProjectMemberActive && viewer.Status != ProjectMemberSuspended {
		return access, ErrProjectForbidden
	}
	return access, nil
}
func ApplyProjectLogAccess(tx *gorm.DB, access ProjectLogAccess) *gorm.DB {
	if access.Project.ID <= 0 || access.Viewer.UserID <= 0 || access.Viewer.ProjectID != access.Project.ID {
		return tx.Where("1 = 0")
	}
	tx = ProjectLogScope(tx, access.Project)
	if !access.Viewer.IsManager() {
		if access.Viewer.Status != ProjectMemberActive && access.Viewer.Status != ProjectMemberSuspended {
			return tx.Where("1 = 0")
		}
		tx = tx.Where("logs.user_id = ?", access.Viewer.UserID)
	}
	if access.ActorID != nil {
		tx = tx.Where("logs.user_id = ?", *access.ActorID)
	}
	if access.TokenID > 0 {
		tx = tx.Where("logs.token_id = ?", access.TokenID)
	}
	return tx
}

type ProjectLogFilter struct {
	Type                                     int
	Start, End                               int64
	Model, TokenName, Group, Reason, Keyword string
	Rejected                                 bool
	EffortClamped                            bool
}

func ProjectLogsQuery(access ProjectLogAccess, filter ProjectLogFilter) *gorm.DB {
	query := ApplyProjectLogAccess(TelemetryDB().Model(&Log{}).Where("logs.type <> ?", LogTypeUnknown), access)
	if filter.Rejected {
		query = query.Where("logs.type = ?", LogTypeError)
	} else if filter.Type != LogTypeUnknown {
		query = query.Where("logs.type = ?", filter.Type)
	}
	if filter.EffortClamped {
		query = query.Where("logs.type = ? AND "+effortClampedCondition(TelemetryDB()), LogTypeConsume)
	}
	if filter.Start > 0 {
		query = query.Where("logs.created_at >= ?", filter.Start)
	}
	if filter.End > 0 {
		query = query.Where("logs.created_at <= ?", filter.End)
	}
	if filter.Model != "" {
		query = query.Where("logs.model_name LIKE ?", filter.Model)
	}
	if filter.TokenName != "" {
		query = query.Where("logs.token_name = ?", filter.TokenName)
	}
	if filter.Group != "" {
		query = query.Where("logs."+logGroupCol+" = ?", filter.Group)
	}
	if filter.Reason != "" {
		query = query.Where("COALESCE(NULLIF(logs.rejection_reason,''),'unknown') = ?", filter.Reason)
	}
	if filter.Keyword != "" {
		query = query.Where("(logs.request_id = ? OR logs.content LIKE ?)", filter.Keyword, filter.Keyword+"%")
	}
	return query
}

// ListProjectLogsContext keeps the exact count independent of page limits.
func ListProjectLogsContext(ctx context.Context, access ProjectLogAccess, filter ProjectLogFilter, offset, limit int) ([]*Log, int64, error) {
	var total int64
	if err := ProjectLogsQuery(access, filter).WithContext(ctx).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	logs, err := ListProjectLogPageContext(ctx, access, filter, offset, limit)
	return logs, total, err
}

func ListProjectLogPageContext(ctx context.Context, access ProjectLogAccess, filter ProjectLogFilter, offset, limit int) ([]*Log, error) {
	query := ProjectLogsQuery(access, filter).WithContext(ctx)
	var logs []*Log
	err := projectLogPageQuery(query, offset, limit).Find(&logs).Error
	return logs, err
}

func projectLogPageQuery(query *gorm.DB, offset, limit int) *gorm.DB {
	return orderLogsNewestFirst(query).Offset(offset).Limit(limit)
}

// SearchProjectLogs finds the request with this id among the project's logs
// the viewer may see, and those whose content starts with the keyword within
// the filter's window, or the last day when it has none.
func SearchProjectLogs(ctx context.Context, access ProjectLogAccess, filter ProjectLogFilter) ([]*Log, error) {
	keyword := filter.Keyword
	filter.Keyword = ""
	since := int64(0)
	if filter.Start <= 0 {
		since = time.Now().Add(-logSearchWindow).Unix()
	}
	return searchLogs(func() *gorm.DB { return ProjectLogsQuery(access, filter).WithContext(ctx) }, keyword, since, false)
}

type ProjectLogView struct {
	Log
	ProjectPublicID string          `json:"project_id"`
	PayerPublicID   string          `json:"payer_user_id"`
	Actor           *ProjectUserRef `json:"actor"`
	UserPublicID    *string         `json:"user_id"`
	TokenPublicID   *string         `json:"token_id"`
}

func ProjectLogViews(project Project, logs []*Log) ([]ProjectLogView, error) {
	users, tokens, err := readProjectLogIdentities(project, logs)
	if err != nil {
		return nil, err
	}
	payer := users[project.OwnerUserID]
	result := make([]ProjectLogView, 0, len(logs))
	for _, log := range logs {
		view := ProjectLogView{Log: *log, ProjectPublicID: project.PublicID, PayerPublicID: payer.PublicID}
		formatUserLogs([]*Log{&view.Log})
		if log.UserId > 0 {
			actor := users[log.UserId]
			view.Actor = &ProjectUserRef{ID: actor.PublicID, Username: log.Username}
			view.UserPublicID = &actor.PublicID
		}
		if log.TokenId > 0 {
			token := tokens[log.TokenId]
			if token.PublicID != "" {
				view.TokenPublicID = &token.PublicID
			}
		}
		result = append(result, view)
	}
	return result, nil
}
func ProjectLogsStat(access ProjectLogAccess, filter ProjectLogFilter) (Stat, error) {
	var result Stat
	filter.Type = LogTypeConsume
	filter.Rejected = false
	if err := ProjectLogsQuery(access, filter).Select("COALESCE(SUM(amount_microusd),0) AS amount_microusd").Scan(&result).Error; err != nil {
		return result, err
	}
	filter.Start = time.Now().Add(-time.Minute).Unix()
	filter.End = 0
	err := ProjectLogsQuery(access, filter).Select("COUNT(*) AS rpm, COALESCE(SUM(prompt_tokens + completion_tokens),0) AS tpm").Scan(&result).Error
	return result, err
}
func ProjectUsageData(access ProjectLogAccess, start, end int64) ([]UsageData, error) {
	rows := []UsageData{}
	query := ProjectLogsQuery(access, ProjectLogFilter{Type: LogTypeConsume, Start: start, End: end})
	hour := "logs.created_at - (logs.created_at % 3600)"
	err := query.Select("logs.user_id, logs.username, logs.model_name, " + hour + " AS created_at, COUNT(*) AS count, COALESCE(SUM(prompt_tokens + completion_tokens),0) AS token_used, COALESCE(SUM(amount_microusd),0) AS amount_microusd").Group("logs.user_id, logs.username, logs.model_name, " + hour).Order("created_at ASC").Scan(&rows).Error
	return rows, err
}
func ReadProjectLedgerSpend(projectID, actorID int, start, end int64) (billing.MicroUSD, error) {
	var amount billing.MicroUSD
	query := CoreDB().Model(&ProjectSpend{}).Where("project_id = ? AND occurred_at >= ? AND occurred_at < ?", projectID, time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC())
	if actorID > 0 {
		query = query.Where("actor_user_id = ?", actorID)
	}
	err := query.Select("COALESCE(SUM(amount_microusd),0)").Scan(&amount).Error
	return amount, err
}

func ProjectAuxTokenScope(tx *gorm.DB, access ProjectLogAccess) *gorm.DB {
	tokens := CoreDB().Unscoped().Model(&Token{}).Select("id").Where("project_id = ?", access.Project.ID)
	if !access.Viewer.IsManager() {
		tokens = tokens.Where("user_id = ?", access.Viewer.UserID)
	}
	if access.TokenID > 0 {
		tokens = tokens.Where("id = ?", access.TokenID)
	}
	if access.ActorID != nil {
		tokens = tokens.Where("user_id = ?", *access.ActorID)
	}
	return tx.Where("token_id IN (?)", tokens)
}
func BuildProjectDashboardUsage(access ProjectLogAccess, start, end int64) (DashboardUsage, error) {
	return BuildProjectDashboardUsageContext(context.Background(), access, start, end)
}

// BuildProjectDashboardUsageContext answers every project-scoped usage page:
// the wallet and each member's own usage. PostgreSQL reads hourly usage
// slices; the busiest project's thirty days of logs took about 3 s on
// 2026-10-16.
func BuildProjectDashboardUsageContext(ctx context.Context, access ProjectLogAccess, start, end int64) (DashboardUsage, error) {
	window := normalizeDashboardUsageWindow(start, end)
	result := DashboardUsage{Role: access.Viewer.Role, Scope: "project", Window: window, ByModel: []DashboardUsageDimensionRow{}, ByKey: []DashboardUsageDimensionRow{}, TimeSeries: []DashboardUsageTimeRow{}}
	base := func() *gorm.DB {
		return ApplyProjectLogAccess(dashboardUsageBaseQuery(window).WithContext(ctx), access)
	}
	if usageSlicesAvailable() {
		if err := buildProjectDashboardUsageFromSlicesContext(ctx, access, window, &result); err != nil {
			return result, err
		}
		return redactProjectDashboardUsage(result), nil
	}
	if routingFactsAreViews(TelemetryDB()) {
		userLimit := 0
		if access.Viewer.IsManager() {
			userLimit = 100
		}
		logs, err := scanPostgresDashboardLogUsage(base(), userLimit)
		if err != nil {
			return result, err
		}
		result.Totals, result.ByModel, result.ByKey, result.ByUser, result.TimeSeries = logs.Totals, logs.ByModel, logs.ByKey, logs.ByUser, logs.TimeSeries
	} else {
		if err := scanDashboardUsageTotals(base(), &result.Totals, true); err != nil {
			return result, err
		}
		var err error
		name := "COALESCE(NULLIF(logs.model_name,''),'unknown')"
		result.ByModel, err = scanDashboardUsageRows(base(), "0", name, name, 20)
		if err != nil {
			return result, err
		}
		name = "COALESCE(NULLIF(logs.token_name,''),'unknown')"
		result.ByKey, err = scanDashboardUsageRows(base(), "logs.token_id", name, "logs.token_id, "+name, 50)
		if err != nil {
			return result, err
		}
		if access.Viewer.IsManager() {
			name = "COALESCE(NULLIF(logs.username,''),'unknown')"
			result.ByUser, err = scanDashboardUsageRows(base(), "logs.user_id", name, "logs.user_id, "+name, 100)
			if err != nil {
				return result, err
			}
		}
		result.TimeSeries, err = scanDashboardUsageTimeRows(base())
		if err != nil {
			return result, err
		}
	}

	if err := ProjectLogsQuery(access, ProjectLogFilter{Type: LogTypeError, Start: window.StartTimestamp, End: window.EndTimestamp}).WithContext(ctx).Count(&result.Totals.ErrorCount).Error; err != nil {
		return result, err
	}
	return redactProjectDashboardUsage(result), nil
}

func redactProjectDashboardUsage(result DashboardUsage) DashboardUsage {
	result.Totals.OutcomeCount = result.Totals.RequestCount + result.Totals.ErrorCount
	// Project managers remain customers; vendor costs/margin and routing-channel
	// identities are platform-only, regardless of their platform account role.
	result.Totals.UpstreamMicrousd = 0
	result.Totals.GrossMicrousd = 0
	result.Totals.GrossMarginRatio = 0
	result.Totals.CostSampleCount = 0
	for _, rows := range [][]DashboardUsageDimensionRow{result.ByModel, result.ByKey, result.ByUser} {
		for i := range rows {
			rows[i].UpstreamMicrousd = 0
			rows[i].GrossMicrousd = 0
			rows[i].GrossMarginRatio = 0
			rows[i].CostSampleCount = 0
		}
	}
	for i := range result.TimeSeries {
		result.TimeSeries[i].UpstreamMicrousd = 0
		result.TimeSeries[i].GrossMicrousd = 0
		result.TimeSeries[i].CostSampleCount = 0
	}
	return result
}

// Customer log views preserve request/usage facts, never procurement or routing internals.
func redactProjectLogEconomics(value interface{}) {
	switch node := value.(type) {
	case map[string]interface{}:
		for key, child := range node {
			name := strings.ToLower(key)
			if name == "admin_info" || strings.HasPrefix(name, "provider_") || strings.HasPrefix(name, "upstream_") || strings.HasPrefix(name, "gross_") || strings.Contains(name, "channel") || strings.HasPrefix(name, "routing_") || name == "protocol_upstream" {
				delete(node, key)
			} else {
				redactProjectLogEconomics(child)
			}
		}
	case []interface{}:
		for _, child := range node {
			redactProjectLogEconomics(child)
		}
	}
}

// redactCustomerLog removes platform accounting and routing data from every customer log surface.
func redactCustomerLog(log *Log) {
	log.ChannelId, log.ChannelName, log.UsageFact = 0, "", ""
	other, _ := common.StrToMap(log.Other)
	redactProjectLogEconomics(other)
	log.Other = common.MapToJsonStr(other)
}
