package service

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm/clause"
)

type monthlyReportAggregate struct {
	TotalRequests int64
	TotalTokens   int64
	TotalMicrousd int64
}

// GenerateMonthlyReport aggregates usage logs and stores a monthly report.
func GenerateMonthlyReport(userID int, year int, month int) (*model.MonthlyReport, error) {
	if userID == 0 || year <= 0 || month <= 0 || month > 12 {
		return nil, errors.New("invalid report period")
	}

	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)

	var agg monthlyReportAggregate
	if model.TelemetryDB() != nil {
		if err := model.TelemetryDB().Table("logs").
			Select("COUNT(*) as total_requests, SUM(prompt_tokens + completion_tokens) as total_tokens, SUM(amount_microusd) as total_microusd").
			Where("type = ? AND user_id = ? AND created_at >= ? AND created_at < ?", model.LogTypeConsume, userID, start.Unix(), end.Unix()).
			Scan(&agg).Error; err != nil {
			return nil, err
		}
	}

	summary := map[string]any{
		"period":       start.Format("2006-01"),
		"generated_at": time.Now().Format(time.RFC3339),
		"overview": map[string]any{
			"total_requests": agg.TotalRequests,
			"total_tokens":   agg.TotalTokens,
			"total_microusd": agg.TotalMicrousd,
		},
	}

	payload, err := json.Marshal(summary)
	if err != nil {
		return nil, err
	}

	report := model.MonthlyReport{
		UserID:  userID,
		Year:    year,
		Month:   month,
		Summary: model.JSONValue(payload),
		Status:  "completed",
	}

	if err := model.CoreDB().Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "year"},
			{Name: "month"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"summary":    report.Summary,
			"status":     report.Status,
			"updated_at": time.Now(),
		}),
	}).Create(&report).Error; err != nil {
		return nil, err
	}

	var saved model.MonthlyReport
	if err := model.CoreDB().Where("user_id = ? AND year = ? AND month = ?", userID, year, month).
		Order("id desc").
		First(&saved).Error; err != nil {
		return nil, err
	}
	return &saved, nil
}
