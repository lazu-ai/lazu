package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
)

func GetModelRankings(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	order := c.DefaultQuery("order", "final")

	list, err := model.ListModelRankings(limit, order)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, list)
}

func RecalculateModelRankings(c *gin.Context) {
	if err := model.UpdateAllRankScores(); err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"status": "ok"})
}
