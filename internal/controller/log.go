package controller

import (
	"net/http"
	"strconv"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"

	"github.com/gin-gonic/gin"
)

// logFailure answers a log, usage or report route.
func logFailure(c *gin.Context, err error) {
	writeDomainFailure(c, err, "")
}

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	userID, _ := strconv.Atoi(c.Query("user_id"))
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	logs, total, atLeast, err := model.GetAllLogs(logType, startTimestamp, endTimestamp, modelName, username, userID, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), channel, group)
	if err != nil {
		logFailure(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.TotalAtLeast = atLeast
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	page := common.GetPageQuery(c)
	logs, total, atLeast, err := model.ListProjectLogsCappedContext(c.Request.Context(), access, projectLogFilter(c), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		logFailure(c, err)
		return
	}
	items, err := model.ProjectLogViews(access.Project, logs)
	if err != nil {
		logFailure(c, err)
		return
	}
	page.SetTotal(int(total))
	page.TotalAtLeast = atLeast
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

func SearchAllLogs(c *gin.Context) {
	keyword := c.Query("keyword")
	logs, err := model.SearchAllLogs(keyword)
	if err != nil {
		logFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
	return
}

func SearchUserLogs(c *gin.Context) {
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	logs, err := model.SearchProjectLogs(c.Request.Context(), access, projectLogFilter(c))
	if err != nil {
		logFailure(c, err)
		return
	}
	items, err := model.ProjectLogViews(access.Project, logs)
	if err != nil {
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, items)
}

func GetLogsStat(c *gin.Context) {
	start, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	userID, _ := strconv.Atoi(c.Query("user_id"))
	channel, _ := strconv.Atoi(c.Query("channel"))
	stat, err := model.AdminLogsRangeStat(model.AdminLogFilter{Start: start, End: end, ModelName: c.Query("model_name"), Username: c.Query("username"),
		TokenName: c.Query("token_name"), Group: c.Query("group"), UserID: userID, Channel: channel})
	if err != nil {
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, stat)
}

func GetLogsSelfStat(c *gin.Context) {
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	stat, err := model.ProjectLogsRangeStat(access, projectLogFilter(c))
	if err != nil {
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, stat)
}

func DeleteHistoryLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		httperr.Invalid(c, "target_timestamp")
		return
	}
	count, err := model.DeleteOldLog(c.Request.Context(), targetTimestamp, 100)
	if err != nil {
		logFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
	return
}
