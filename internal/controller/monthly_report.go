package controller

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"gorm.io/gorm"
)

func ListMonthlyReports(c *gin.Context) {
	userID := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	var reports []model.MonthlyReport
	var total int64
	query := model.CoreDB().Model(&model.MonthlyReport{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	if err := query.Order("year desc, month desc").
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Find(&reports).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	pageInfo.SetItems(reports)
	pageInfo.SetTotal(int(total))
	common.ApiSuccess(c, pageInfo)
}

func GetMonthlyReportLatest(c *gin.Context) {
	userID := c.GetInt("id")
	var report model.MonthlyReport
	if err := model.CoreDB().Where("user_id = ?", userID).Order("year desc, month desc").First(&report).Error; err != nil {
		// No report yet is an empty state, not a failure.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiSuccess(c, nil)
			return
		}
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

func GenerateMonthlyReport(c *gin.Context) {
	userID := c.GetInt("id")
	year, yearErr := strconv.Atoi(c.DefaultQuery("year", "0"))
	month, monthErr := strconv.Atoi(c.DefaultQuery("month", "0"))
	if yearErr != nil || year < 0 {
		httperr.Invalid(c, "year")
		return
	}
	if monthErr != nil || month < 0 || month > 12 {
		httperr.Invalid(c, "month")
		return
	}
	if year == 0 || month == 0 {
		now := time.Now().AddDate(0, -1, 0)
		year = now.Year()
		month = int(now.Month())
	}
	report, err := service.GenerateMonthlyReport(userID, year, month)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

func GetMonthlyReportSubscription(c *gin.Context) {
	userID := c.GetInt("id")
	var sub model.ReportSubscription
	if err := model.CoreDB().Where("user_id = ?", userID).First(&sub).Error; err != nil {
		common.ApiSuccess(c, nil)
		return
	}
	common.ApiSuccess(c, sub)
}

func UpdateMonthlyReportSubscription(c *gin.Context) {
	userID := c.GetInt("id")
	var req model.ReportSubscription
	if err := c.ShouldBindJSON(&req); err != nil {
		httperr.Invalid(c, "body")
		return
	}
	req.UserID = userID
	if err := model.CoreDB().Where("user_id = ?", userID).
		Assign(req).
		FirstOrCreate(&req).Error; err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, req)
}
