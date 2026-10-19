package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func GetUserDashboardUsage(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	role := resolveDashboardScopeRole(c.GetInt("role"), c.Query("scope"))
	channelID, _ := strconv.Atoi(c.Query("channel"))
	userID, _ := strconv.Atoi(c.Query("user_id"))
	userFilters := model.DashboardUsageLogFilters{
		ChannelID: channelID,
		Group:     c.Query("group"),
		UserID:    userID,
		Username:  c.Query("username"),
	}
	// The usage log's 用户 tab needs only the platform user rows; building
	// the whole dashboard for it read every routing outcome of the range.
	if c.Query("view") == "users" && c.Query("project_id") == "" && role >= common.RoleRootUser {
		rows, err := model.BuildAdminDashboardUsersContext(c.Request.Context(), userFilters, startTimestamp, endTimestamp)
		if err != nil {
			logFailure(c, err)
			return
		}
		common.ApiSuccess(c, model.DashboardUsage{Role: "admin", Scope: "global", ByUser: rows})
		return
	}
	var usage model.DashboardUsage
	var err error
	var projectAccess *model.ProjectLogAccess
	if c.Query("project_id") != "" || role < common.RoleRootUser {
		access, ok := projectLogAccess(c)
		if !ok {
			return
		}
		projectAccess = &access
		usage, err = model.BuildProjectDashboardUsageContext(c.Request.Context(), access, startTimestamp, endTimestamp)
	} else {
		usage, err = model.BuildDashboardUsageForRoleContext(c.Request.Context(), c.GetInt("id"), role, startTimestamp, endTimestamp)
	}
	if err != nil {
		logFailure(c, err)
		return
	}
	if projectAccess == nil && role >= common.RoleRootUser && (userFilters.UserID > 0 || strings.TrimSpace(userFilters.Username) != "" || strings.TrimSpace(userFilters.Group) != "" || userFilters.ChannelID > 0) {
		usage.ByUser, err = model.BuildAdminDashboardUserRowsContext(c.Request.Context(), userFilters, startTimestamp, endTimestamp)
		if err != nil {
			logFailure(c, err)
			return
		}
	}
	common.ApiSuccess(c, usage)
}

func GetSelfDashboardUsage(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	usage, err := model.BuildProjectDashboardUsageContext(c.Request.Context(), access, startTimestamp, endTimestamp)
	if err != nil {
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, usage)
}

func ExportUserUsage(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	format := strings.ToLower(strings.TrimSpace(c.Query("format")))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "xlsx" {
		httperr.Code(c, types.ErrorCodeUsageExportFormatInvalid, types.D{"field": "format", "format": format})
		return
	}
	dataset := strings.ToLower(strings.TrimSpace(c.Query("dataset")))
	role := resolveDashboardScopeRole(c.GetInt("role"), c.Query("scope"))
	logType, _ := strconv.Atoi(c.Query("type"))
	channelID, _ := strconv.Atoi(c.Query("channel"))
	userID, _ := strconv.Atoi(c.Query("user_id"))
	logFilters := model.DashboardUsageLogFilters{
		ChannelID: channelID,
		Group:     c.Query("group"),
		LogType:   logType,
		ModelName: c.Query("model_name"),
		TokenName: c.Query("token_name"),
		UserID:    userID,
		Username:  c.Query("username"),
	}
	var usage model.DashboardUsage
	var err error
	var projectAccess *model.ProjectLogAccess
	if c.Query("project_id") != "" || role < common.RoleRootUser {
		access, ok := projectLogAccess(c)
		if !ok {
			return
		}
		projectAccess = &access
	}
	// A log export needs only the window; building the dashboard for it read
	// every routing outcome of the range.
	switch {
	case dataset == "logs":
		usage.Window = model.DashboardUsageWindowFor(startTimestamp, endTimestamp)
	case projectAccess != nil:
		usage, err = model.BuildProjectDashboardUsageContext(c.Request.Context(), *projectAccess, startTimestamp, endTimestamp)
	default:
		usage, err = model.BuildDashboardUsageForRoleContext(c.Request.Context(), c.GetInt("id"), role, startTimestamp, endTimestamp)
	}
	if err != nil {
		logFailure(c, err)
		return
	}

	switch format {
	case "csv":
		var payload []byte
		var err error
		prefix := "usage"
		if dataset == "logs" {
			logs, logErr := listExportUsageLogs(c, projectAccess, role, usage.Window, logFilters)
			if logErr != nil {
				logFailure(c, logErr)
				return
			}
			payload, err = model.BuildUsageLogsCSV(logs)
			prefix = "usage-logs"
		} else {
			payload, err = model.BuildDashboardUsageCSV(usage)
		}
		if err != nil {
			logFailure(c, err)
			return
		}
		filename := model.UsageFilename(prefix, usage.Window, "csv")
		c.Header("Content-Disposition", "attachment; filename="+filename)
		c.Data(http.StatusOK, "text/csv; charset=utf-8", payload)
	default: // xlsx
		var payload []byte
		var err error
		prefix := "usage"
		if dataset == "logs" {
			logs, logErr := listExportUsageLogs(c, projectAccess, role, usage.Window, logFilters)
			if logErr != nil {
				logFailure(c, logErr)
				return
			}
			payload, err = model.BuildUsageLogsXLSX(logs)
			prefix = "usage-logs"
		} else {
			payload, err = model.BuildDashboardUsageXLSX(usage)
		}
		if err != nil {
			logFailure(c, err)
			return
		}
		filename := model.UsageFilename(prefix, usage.Window, "xlsx")
		c.Header("Content-Disposition", "attachment; filename="+filename)
		c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", payload)
	}
}

func resolveDashboardScopeRole(sessionRole int, requestedScope string) int {
	if strings.EqualFold(strings.TrimSpace(requestedScope), "self") {
		return common.RoleCommonUser
	}
	return sessionRole
}

func listExportUsageLogs(c *gin.Context, access *model.ProjectLogAccess, role int, window model.DashboardUsageWindow, filters model.DashboardUsageLogFilters) ([]model.Log, error) {
	if access == nil {
		return model.ListDashboardUsageLogsContext(c.Request.Context(), c.GetInt("id"), role, window, filters, 50000)
	}
	filter := projectLogFilter(c)
	filter.Start = window.StartTimestamp
	filter.End = window.EndTimestamp
	var rows []*model.Log
	if err := model.ProjectLogsQuery(*access, filter).WithContext(c.Request.Context()).Order("logs.created_at ASC, logs.id ASC").Limit(50000).Find(&rows).Error; err != nil {
		return nil, err
	}
	views, err := model.ProjectLogViews(access.Project, rows)
	if err != nil {
		return nil, err
	}
	logs := make([]model.Log, 0, len(views))
	for _, view := range views {
		logs = append(logs, view.Log)
	}
	return logs, nil
}
