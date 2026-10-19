package model

import (
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrProjectDeletionDependency = types.Err(types.ErrorCodeProjectDeletionDependency)

// Lock projects before the user, matching invitations/settings/Key changes.
// Account deletion cannot strand another member or a pending payment/refund,
// including dependencies of suspended members.
func checkUserProjectDeletionTx(tx *gorm.DB, userID int, hard bool) error {
	if !tx.Migrator().HasTable(&Project{}) {
		return nil
	}
	var projects []Project
	memberships := tx.Model(&ProjectMember{}).Select("project_id").Where("user_id = ?", userID)
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_user_id = ? OR id IN (?)", userID, memberships).Order("id ASC").Find(&projects).Error; err != nil {
		return err
	}
	exists := func(query *gorm.DB) (bool, error) {
		var count int64
		err := query.Count(&count).Error
		return count > 0, err
	}
	for _, project := range projects {
		if project.OwnerUserID != userID {
			if hard {
				return ErrProjectDeletionDependency
			}
			continue
		}
		if hard {
			// Retain an owned project while any dependent row exists, including
			// disabled budgets, revoked credentials and historical operations.
			for _, table := range []interface{}{&Token{}, &SpendBudget{}, &SpendBudgetCharge{}, &SpendBudgetReservation{}, &SpendBudgetAlert{}, &ProjectSpend{}, &ProjectInvite{}, &BudgetRequest{}, &ProjectRemoval{}, &ProjectBudgetHistory{}, &ProjectNotification{}, &TopUp{}, &MediaJob{}, &File{}} {
				if !tx.Migrator().HasTable(table) {
					continue
				}
				found, err := exists(tx.Unscoped().Model(table).Where("project_id = ?", project.ID))
				if err != nil {
					return err
				}
				if found {
					return ErrProjectDeletionDependency
				}
			}
			found, err := exists(tx.Model(&ProjectMember{}).Where("project_id = ? AND user_id <> ?", project.ID, userID))
			if err != nil {
				return err
			}
			if found {
				return ErrProjectDeletionDependency
			}
		}
		queries := []*gorm.DB{tx.Model(&ProjectMember{}).Where("project_id = ? AND user_id <> ? AND status IN ?", project.ID, userID, []string{ProjectMemberActive, ProjectMemberSuspended})}
		if tx.Migrator().HasTable(&ProjectInvite{}) {
			queries = append(queries, tx.Model(&ProjectInvite{}).Where("project_id = ? AND status = ?", project.ID, "pending"))
		}
		for _, query := range queries {
			found, err := exists(query)
			if err != nil {
				return err
			}
			if found {
				return ErrProjectDeletionDependency
			}
		}
	}
	for _, table := range []string{"settlement_outboxes", "settlement_failures"} {
		if !tx.Migrator().HasTable(table) {
			continue
		}
		found, err := exists(tx.Table(table).Where("(user_id = ? OR actor_user_id = ?) AND status <> ?", userID, userID, SettlementFailureStatusResolved))
		if err != nil {
			return err
		}
		if found {
			return ErrProjectDeletionDependency
		}
	}
	if tx.Migrator().HasTable(&TopUp{}) {
		query := tx.Model(&TopUp{}).Where("user_id = ? OR paid_by_user_id = ?", userID, userID)
		if !hard {
			query = query.Where("status = ?", common.TopUpStatusPending)
		}
		found, err := exists(query)
		if err != nil {
			return err
		}
		if found {
			return ErrProjectDeletionDependency
		}
	}
	if tx.Migrator().HasTable(&MediaJob{}) {
		var jobs []MediaJob
		query := tx.Where("user_id = ? OR payer_user_id = ?", userID, userID)
		if !hard {
			query = query.Where("billing_settled_at = 0")
		}
		if err := query.Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if hard || job.Status == MediaJobStatusPending || job.Status == MediaJobStatusRunning || MediaJobBillingReservationAmount(job) > 0 {
				return ErrProjectDeletionDependency
			}
		}
	}
	if hard {
		var user User
		if err := tx.Unscoped().Select("quota").First(&user, userID).Error; err != nil {
			return err
		}
		if user.Quota != 0 {
			return ErrProjectDeletionDependency
		}
		if tx.Migrator().HasTable(&WalletLedger{}) {
			found, err := exists(tx.Model(&WalletLedger{}).Where("user_id = ?", userID))
			if err != nil {
				return err
			}
			if found {
				return ErrProjectDeletionDependency
			}
		}
		for _, entry := range []struct {
			model interface{}
			where string
		}{
			{&File{}, "user_id = ? OR payer_user_id = ?"}, {&ProjectSpend{}, "actor_user_id = ? OR payer_user_id = ?"}, {&ProjectBudgetHistory{}, "user_id = ? OR changed_by = ?"},
		} {
			if tx.Migrator().HasTable(entry.model) {
				found, err := exists(tx.Model(entry.model).Where(entry.where, userID, userID))
				if err != nil {
					return err
				}
				if found {
					return ErrProjectDeletionDependency
				}
			}
		}
	}
	return nil
}
func finishUserProjectDeletionTx(tx *gorm.DB, userID int, hard bool) error {
	if err := runUserDeletingHooks(tx, userID); err != nil {
		return err
	}
	if err := deleteUserAttributionTx(tx, userID); err != nil {
		return err
	}
	if CoreDB() == TelemetryDB() {
		if err := deleteUserSessionContentTx(tx, userID); err != nil {
			return err
		}
	} else {
		if err := tx.Create(&TelemetryCleanupJob{UserID: userID}).Error; err != nil {
			return err
		}
	}
	if !tx.Migrator().HasTable(&Project{}) {
		return nil
	}
	if hard {
		if err := tx.Where("user_id = ?", userID).Delete(&ProjectMember{}).Error; err != nil {
			return err
		}
		return tx.Where("owner_user_id = ?", userID).Delete(&Project{}).Error
	}
	return tx.Model(&ProjectMember{}).Where("user_id = ?", userID).Updates(map[string]interface{}{"status": ProjectMemberRemoved, "removed_at": time.Now().Unix(), "version": gorm.Expr("version + 1")}).Error
}
