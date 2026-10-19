package controller

import (
	"strconv"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// Sessions are the tasks agents ran through the gateway (see
// model/agent_session.go). Customers see their own; admins see everyone's
// and can narrow to one account with ?user_id=.

func GetAllLogSessions(c *gin.Context) {
	query := agentSessionQuery(c)
	query.UserID, _ = strconv.Atoi(c.Query("user_id"))
	listAgentSessions(c, query)
}

func GetUserLogSessions(c *gin.Context) {
	query := agentSessionQuery(c)
	query.UserID = c.GetInt("id")
	listAgentSessions(c, query)
}

func listAgentSessions(c *gin.Context, query model.AgentSessionQuery) {
	pageInfo := common.GetPageQuery(c)
	query.StartIdx = pageInfo.GetStartIdx()
	query.Num = pageInfo.GetPageSize()
	items, total, summary, err := model.ListAgentSessions(query)
	if err != nil {
		logFailure(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, gin.H{"page": pageInfo, "summary": summary})
}

func GetAllLogSessionDetail(c *gin.Context) {
	agentSessionDetail(c, 0)
}

func GetUserLogSessionDetail(c *gin.Context) {
	agentSessionDetail(c, c.GetInt("id"))
}

func agentSessionDetail(c *gin.Context, userID int) {
	id := strings.TrimSpace(c.Param("session_id"))
	if id == "" {
		httperr.Invalid(c, "session_id")
		return
	}
	var afterTime int64
	var afterID int
	if cursor := c.Query("request_cursor"); cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 {
			httperr.Invalid(c, "request_cursor")
			return
		}
		var err error
		afterTime, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || afterTime < 0 {
			httperr.Invalid(c, "request_cursor")
			return
		}
		afterID, err = strconv.Atoi(parts[1])
		if err != nil || afterID <= 0 {
			httperr.Invalid(c, "request_cursor")
			return
		}
	}
	detail, err := model.GetAgentSessionDetailPage(userID, id, afterTime, afterID)
	if err != nil {
		writeDomainFailure(c, err, types.ErrorCodeLogSessionNotFound)
		return
	}
	if userID == 0 {
		// Reading another person's first message is an audited act.
		model.WriteAdminAuditLog(c, "session.view", "agent_session", detail.Session.PublicID, detail.Username, nil, nil)
	} else {
		detail.Session.UpstreamCostMicrousd = 0
	}
	common.ApiSuccess(c, detail)
}

// agentSessionQuery reads the list filters. range is 24h / 7d / 30d; an
// explicit start_timestamp / end_timestamp wins.
func agentSessionQuery(c *gin.Context) model.AgentSessionQuery {
	start, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if start == 0 {
		now := time.Now().Unix()
		switch c.DefaultQuery("range", "24h") {
		case "7d":
			start = now - 7*86400
		case "30d":
			start = now - 30*86400
		case "90d":
			start = now - 90*86400
		default:
			start = now - 86400
		}
	}
	return model.AgentSessionQuery{
		Start:      start,
		End:        end,
		Client:     c.Query("client"),
		TokenName:  c.Query("token_name"),
		OnlyFailed: c.Query("failed") == "1" || c.Query("failed") == "true",
		Keyword:    c.Query("keyword"),
	}
}
