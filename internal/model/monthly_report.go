package model

import (
	"fmt"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

type MonthlyReport struct {
	ID     uint64  `json:"id" gorm:"primaryKey"`
	UserID int     `json:"user_id" gorm:"index"`
	OrgID  *uint64 `json:"org_id,omitempty" gorm:"index"`

	Year  int `json:"year"`
	Month int `json:"month"`

	Summary JSONValue `json:"summary" gorm:"type:json"`

	Status      string     `json:"status" gorm:"default:generating"`
	EmailSent   bool       `json:"email_sent"`
	EmailSentAt *time.Time `json:"email_sent_at,omitempty"`
	PDFURL      string     `json:"pdf_url,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const monthlyReportUniqueIndexName = "uk_monthly_reports_user_year_month"

type monthlyReportDuplicateGroup struct {
	UserID int   `gorm:"column:user_id"`
	Year   int   `gorm:"column:year"`
	Month  int   `gorm:"column:month"`
	Count  int64 `gorm:"column:cnt"`
}

// EnsureMonthlyReportUniqConstraint ensures monthly reports keep a single
// row per (user_id, year, month), and creates a database unique index.
func EnsureMonthlyReportUniqConstraint() error {
	if CoreDB() == nil {
		return nil
	}
	if err := deduplicateMonthlyReports(); err != nil {
		return err
	}
	if CoreDB().Migrator().HasIndex(&MonthlyReport{}, monthlyReportUniqueIndexName) {
		return nil
	}
	if err := CoreDB().Exec(buildMonthlyReportUniqueIndexSQL()).Error; err != nil {
		// Handle races/partial migrations where another process created the index.
		if CoreDB().Migrator().HasIndex(&MonthlyReport{}, monthlyReportUniqueIndexName) {
			return nil
		}
		return err
	}
	return nil
}

func deduplicateMonthlyReports() error {
	var groups []monthlyReportDuplicateGroup
	if err := CoreDB().Model(&MonthlyReport{}).
		Select("user_id, year, month, COUNT(*) as cnt").
		Group("user_id, year, month").
		Having("COUNT(*) > 1").
		Scan(&groups).Error; err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}

	return CoreDB().Transaction(func(tx *gorm.DB) error {
		for _, group := range groups {
			var ids []uint64
			if err := tx.Model(&MonthlyReport{}).
				Where("user_id = ? AND year = ? AND month = ?", group.UserID, group.Year, group.Month).
				Order("updated_at desc").
				Order("id desc").
				Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) <= 1 {
				continue
			}
			if err := tx.Where("id IN ?", ids[1:]).Delete(&MonthlyReport{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func buildMonthlyReportUniqueIndexSQL() string {
	if common.UsingPostgreSQL {
		return fmt.Sprintf(
			`CREATE UNIQUE INDEX %s ON "monthly_reports" ("user_id", "year", "month")`,
			monthlyReportUniqueIndexName,
		)
	}
	return fmt.Sprintf(
		"CREATE UNIQUE INDEX %s ON `monthly_reports` (`user_id`, `year`, `month`)",
		monthlyReportUniqueIndexName,
	)
}

type ReportSubscription struct {
	ID int `json:"id" gorm:"primaryKey"`

	UserID int `json:"user_id" gorm:"uniqueIndex"`

	ReportType string `json:"report_type" gorm:"default:monthly"`
	Enabled    bool   `json:"enabled" gorm:"default:true"`

	NotifyEmail   bool   `json:"notify_email" gorm:"default:true"`
	NotifyWebhook string `json:"notify_webhook,omitempty"`

	IncludePDF bool `json:"include_pdf" gorm:"default:true"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
