package controller

import (
	"net/http"
	"strconv"

	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// usageDataMaxDays is the widest window the self-service usage chart reads.
const usageDataMaxDays = 30

func GetAllUsageData(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	data, err := model.GetAllUsageData(startTimestamp, endTimestamp, username)
	if err != nil {
		logFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
	return
}

func GetUserUsageData(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if endTimestamp-startTimestamp > usageDataMaxDays*24*3600 {
		httperr.Code(c, types.ErrorCodeUsageRangeTooLong, types.D{"max_days": usageDataMaxDays})
		return
	}
	access, ok := projectLogAccess(c)
	if !ok {
		return
	}
	data, err := model.ProjectUsageData(access, startTimestamp, endTimestamp)
	if err != nil {
		logFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
	return
}
