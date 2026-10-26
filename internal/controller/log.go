package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

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

func GetLogByKey(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		httperr.Code(c, types.ErrorCodeSessionRequired)
		return
	}

	key := normalizeTokenInputKey(c.Query("key"))
	if key == "" {
		httperr.Invalid(c, "key")
		return
	}

	token, err := model.GetTokenByKey(key, false)
	if err != nil {
		credentialFailure(c, err)
		return
	}
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	if _, err := model.ResolveTokenCredential(token.Id, key, time.Now().UTC()); err != nil {
		credentialFailure(c, err)
		return
	}
	if access.Viewer.Status == model.ProjectMemberSuspended {
		if token.ProjectID != access.Project.ID || token.UserId != userID {
			keyFailure(c, model.ErrProjectForbidden)
			return
		}
	} else if _, err := model.CheckProjectTokenAction(model.CoreDB(), access.Project.ID, userID, token.Id, model.ProjectReadPersonalResource); err != nil {
		keyFailure(c, err)
		return
	}
	access.TokenID = token.Id

	pageInfo := common.GetPageQuery(c)
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	rows, total, err := model.ListProjectLogsContext(c.Request.Context(), access,
		model.ProjectLogFilter{Type: model.LogTypeConsume, Start: startTimestamp, End: endTimestamp}, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		logFailure(c, err)
		return
	}
	logs, err := model.ProjectLogViews(access.Project, rows)
	if err != nil {
		logFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"message":   "",
		"data":      logs,
		"total":     total,
		"page":      pageInfo.GetPage(),
		"page_size": pageInfo.GetPageSize(),
	})
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	userID, _ := strconv.Atoi(c.Query("user_id"))
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	stat := model.SumUsedQuota(logType, startTimestamp, endTimestamp, modelName, username, userID, tokenName, channel, group)
	//tokenNum := model.SumUsedToken(logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"amount_microusd": stat.AmountMicrousd,
			"rpm":             stat.Rpm,
			"tpm":             stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	stat, err := model.ProjectLogsStat(access, projectLogFilter(c))
	if err != nil {
		logFailure(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"amount_microusd": stat.AmountMicrousd, "rpm": stat.Rpm, "tpm": stat.Tpm})
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

func GetUserUsedModels(c *gin.Context) {
	userId := c.GetInt("id")
	models, err := model.GetUserUsedModels(userId)
	if err != nil {
		logFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
}
