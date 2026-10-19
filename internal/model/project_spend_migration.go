package model

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The primary database owns this cursor even when logs live in another CoreDB().
// Each batch commits its events and cursor together, so interruption is safe.
type ProjectDataMigration struct {
	Name         string `gorm:"primaryKey;size:64"`
	LastChargeID uint
	LastLogID    int
	MaxChargeID  uint
	MaxLogID     int
	CompletedAt  *time.Time
}

func MigrateProjectSpendHistory() error {
	if CoreDB() == nil || TelemetryDB() == nil {
		return errors.New("project spend migration requires both databases")
	}
	release, err := acquireMigrationLock(CoreDB(), "project_spend_history")
	if err != nil {
		return err
	}
	defer release()
	marker := ProjectDataMigration{Name: "project_spend_history"}
	if err := CoreDB().Model(&SpendBudgetCharge{}).Select("COALESCE(MAX(id),0)").Scan(&marker.MaxChargeID).Error; err != nil {
		return err
	}
	if err := TelemetryDB().Model(&Log{}).Select("COALESCE(MAX(id),0)").Scan(&marker.MaxLogID).Error; err != nil {
		return err
	}
	latestChargeID, latestLogID := marker.MaxChargeID, marker.MaxLogID
	if err := CoreDB().Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; err != nil {
		return err
	}
	if err := CoreDB().First(&marker, "name = ?", marker.Name).Error; err != nil {
		return err
	}
	if marker.CompletedAt != nil {
		if latestChargeID <= marker.LastChargeID && latestLogID <= marker.LastLogID {
			return nil
		}
		marker.MaxChargeID, marker.MaxLogID = latestChargeID, latestLogID
		marker.CompletedAt = nil
		if err := CoreDB().Model(&ProjectDataMigration{}).Where("name = ?", marker.Name).Updates(map[string]interface{}{"max_charge_id": marker.MaxChargeID, "max_log_id": marker.MaxLogID, "completed_at": nil}).Error; err != nil {
			return err
		}
	}
	for {
		var rows []SpendBudgetCharge
		if err := CoreDB().Where("id > ? AND id <= ?", marker.LastChargeID, marker.MaxChargeID).Order("id ASC").Limit(200).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		if err := CoreDB().Transaction(func(tx *gorm.DB) error {
			for _, row := range rows {
				if row.AmountMicrousd > 0 && (row.ProjectID <= 0 || row.RequestID == "") {
					return fmt.Errorf("budget charge %d lacks an unambiguous project/request identity; reconcile attribution before retrying migration", row.ID)
				}
				if row.ProjectID > 0 && row.AmountMicrousd > 0 && row.RequestID != "" {
					input := SpendBudgetChargeInput{ProjectID: row.ProjectID, PayerUserID: row.PayerUserID, ActorUserID: row.ActorUserID, TokenID: row.TokenID, RequestID: row.RequestID, DeltaMicrousd: row.AmountMicrousd, OccurredAt: row.CreatedAt}
					if err := recordProjectSpendTx(tx, input); err != nil {
						return err
					}
				}
				marker.LastChargeID = row.ID
			}
			return tx.Model(&ProjectDataMigration{}).Where("name = ?", marker.Name).Update("last_charge_id", marker.LastChargeID).Error
		}); err != nil {
			return err
		}
	}
	for {
		var rows []Log
		if err := TelemetryDB().Where("id > ? AND id <= ?", marker.LastLogID, marker.MaxLogID).Order("id ASC").Limit(200).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		if err := CoreDB().Transaction(func(tx *gorm.DB) error {
			for _, row := range rows {
				if row.Type == LogTypeConsume && row.AmountMicrousd > 0 && row.ProjectID == 0 {
					projectID, payerID := row.ProjectID, row.PayerUserID
					if projectID == 0 {
						var project Project
						if err := tx.Where("owner_user_id = ?", row.UserId).First(&project).Error; err != nil {
							return fmt.Errorf("legacy usage log %d has no default project: %w", row.Id, err)
						}
						projectID, payerID = project.ID, project.OwnerUserID
					}
					requestID := row.RequestID
					if requestID == "" {
						requestID = fmt.Sprintf("legacy-log:%d", row.Id)
					}
					input := SpendBudgetChargeInput{ProjectID: projectID, PayerUserID: payerID, ActorUserID: row.UserId, TokenID: row.TokenId, RequestID: requestID, DeltaMicrousd: row.AmountMicrousd, OccurredAt: time.Unix(row.CreatedAt, 0).UTC()}
					if err := recordProjectSpendTx(tx, input); err != nil {
						return err
					}
				}
				marker.LastLogID = row.Id
			}
			return tx.Model(&ProjectDataMigration{}).Where("name = ?", marker.Name).Update("last_log_id", marker.LastLogID).Error
		}); err != nil {
			return err
		}
	}
	return CoreDB().Model(&ProjectDataMigration{}).Where("name = ?", marker.Name).Update("completed_at", time.Now().UTC()).Error
}
