package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SignupAttribution records where a registration came from: the campaign
// parameters a registered signup hook supplies, the site that referred the visitor, the page they landed on, an invite or promo code, how
// they signed up and the country Cloudflare placed them in. One row per user,
// automatic fields written once (or filled after an answer-only placeholder). No IP address is stored.
type SignupAttribution struct {
	CampaignID      string    `json:"campaign_id,omitempty" gorm:"size:128"`
	SourcePlatform  string    `json:"source_platform,omitempty" gorm:"size:128"`
	CreativeFormat  string    `json:"creative_format,omitempty" gorm:"size:128"`
	MarketingTactic string    `json:"marketing_tactic,omitempty" gorm:"size:128"`
	VisitorID       string    `json:"visitor_id,omitempty" gorm:"size:36;index"`
	ReferralCode    string    `json:"referral_code,omitempty" gorm:"size:20"`
	UserID          int       `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	CreatedAt       time.Time `json:"created_at" gorm:"index"`
	Method          string    `json:"method" gorm:"size:24;index"`
	Source          string    `json:"source" gorm:"size:128;index"`
	Medium          string    `json:"medium" gorm:"size:128"`
	Campaign        string    `json:"campaign" gorm:"size:128;index"`
	Content         string    `json:"content" gorm:"size:128"`
	Term            string    `json:"term" gorm:"size:128"`
	ReferrerHost    string    `json:"referrer_host" gorm:"size:255;index"`
	LandingPath     string    `json:"landing_path" gorm:"size:512"`
	InviteCode      string    `json:"invite_code" gorm:"size:64;index"`
	Country         string    `json:"country" gorm:"size:8"`
	// FirstSeenAt is when the browser first arrived with these parameters,
	// which can be days before the registration itself.
	FirstSeenAt int64 `json:"first_seen_at"`
	// SelfReported is the person's own answer to "where did you hear about
	// Lazu" (SourceAnswers), or SourceAnswerSkipped. Empty until asked.
	SelfReported   string `json:"self_reported,omitempty" gorm:"size:32;index"`
	SelfReportedAt int64  `json:"self_reported_at,omitempty"`
}

func (SignupAttribution) TableName() string {
	return "signup_attributions"
}

// RecordSignupAttribution stores the attribution for a new account. A second
// complete record is never overwritten. An answer-only placeholder can be
// filled by a later login retry without losing its first survey answer.
func RecordSignupAttribution(row *SignupAttribution) error {
	if row == nil || row.UserID <= 0 || CoreDB() == nil {
		return nil
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	conflict := clause.OnConflict{DoNothing: true}
	if row.Method != "" {
		conflict = clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"campaign_id", "source_platform", "creative_format", "marketing_tactic",
				"visitor_id", "referral_code", "created_at", "method", "source", "medium",
				"campaign", "content", "term", "referrer_host", "landing_path", "invite_code",
				"country", "first_seen_at",
			}),
			Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "COALESCE(signup_attributions.method, '') = ''"}}},
		}
	}
	return CoreDB().Clauses(conflict).Create(row).Error
}

// GetSignupAttribution returns the attribution row for a user, if any.
func GetSignupAttribution(userID int) (*SignupAttribution, error) {
	var row SignupAttribution
	result := CoreDB().Where("user_id = ?", userID).Limit(1).Find(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

// deleteUserAttributionTx removes the account's sign-up attribution with the
// rest of its data.
func deleteUserAttributionTx(tx *gorm.DB, userID int) error {
	if !tx.Migrator().HasTable(&SignupAttribution{}) {
		return nil
	}
	return tx.Where("user_id = ?", userID).Delete(&SignupAttribution{}).Error
}
