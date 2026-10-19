package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"time"
)

func AdminGatewayPerformance(c *gin.Context) {
	end := time.Now().UTC()
	start := end.Truncate(24*time.Hour).AddDate(0, 0, -29)
	days, err := model.GatewayPerformanceDays(c.Request.Context(), start, end)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	byDay := make(map[string]model.GatewayTimingDay, len(days))
	for _, day := range days {
		byDay[day.Day] = day
	}
	calendar := make([]model.GatewayTimingDay, 0, 30)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		point, present := byDay[key]
		if !present {
			point.Day = key
		}
		calendar = append(calendar, point)
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"timezone": "UTC", "days": calendar}})
}
