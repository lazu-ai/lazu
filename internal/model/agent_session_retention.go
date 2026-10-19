package model

import (
	"time"

	"gorm.io/gorm"
)

// PurgeAgentSessionContent removes text, including the list's preview, without
// deleting usage/billing aggregates. It also covers legacy previews whose full
// message row is missing. Both copies are removed in the same transaction.
func PurgeAgentSessionContent(cutoff time.Time) error {
	if TelemetryDB() == nil || cutoff.IsZero() {
		return nil
	}
	return TelemetryDB().Transaction(func(tx *gorm.DB) error {
		expired := tx.Model(&AgentSessionMessage{}).Select("session_pk").Where("created_at < ?", cutoff.UTC())
		if err := tx.Model(&AgentSession{}).
			Where("first_message_preview <> '' AND (started_at < ? OR id IN (?))", cutoff.Unix(), expired).
			Update("first_message_preview", "").Error; err != nil {
			return err
		}
		return tx.Where("created_at < ?", cutoff.UTC()).Delete(&AgentSessionMessage{}).Error
	})
}

func deleteUserSessionContentTx(tx *gorm.DB, userID int) error {
	// Old installations and focused account tests may not have session tables.
	if !tx.Migrator().HasTable(&AgentSession{}) {
		return nil
	}
	if tx.Migrator().HasTable(&AgentSessionMessage{}) {
		sessions := tx.Model(&AgentSession{}).Select("id").Where("user_id = ?", userID)
		if err := tx.Where("session_pk IN (?)", sessions).Delete(&AgentSessionMessage{}).Error; err != nil {
			return err
		}
	}
	return tx.Model(&AgentSession{}).Where("user_id = ?", userID).Update("first_message_preview", "").Error
}

// TelemetryCleanupJob records cross-store privacy cleanup in the same core
// transaction that deletes an account. A jobs retry completes it idempotently.
type TelemetryCleanupJob struct {
	ID          uint       `gorm:"primaryKey"`
	UserID      int        `gorm:"not null;index"`
	CompletedAt *time.Time `gorm:"index"`
	CreatedAt   time.Time
}

func DrainTelemetryCleanupJobs() error {
	if CoreDB() == nil || TelemetryDB() == nil {
		return nil
	}
	var jobs []TelemetryCleanupJob
	if err := CoreDB().Where("completed_at IS NULL").Order("id").Limit(500).Find(&jobs).Error; err != nil {
		return err
	}
	for _, job := range jobs {
		if err := TelemetryDB().Transaction(func(tx *gorm.DB) error { return deleteUserSessionContentTx(tx, job.UserID) }); err != nil {
			return err
		}
		if err := CoreDB().Model(&TelemetryCleanupJob{}).Where("id = ? AND completed_at IS NULL", job.ID).Update("completed_at", time.Now().UTC()).Error; err != nil {
			return err
		}
	}
	return nil
}
