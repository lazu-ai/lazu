package model

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lazu-ai/lazu/internal/common"

	"gorm.io/gorm"
)

// migrateTeamProjects preserves identifiers and accounting history. Re-running
// it only fills missing attribution; it never reassigns shared resources or
// overwrites project settings, membership, or wallet balances.
func migrateTeamProjects(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("project migration: database is not initialized")
	}
	if err := backfillIntPublicIDs(db, "top_ups", "top"); err != nil {
		return err
	}
	before, err := readProjectMigrationAudit(db)
	if err != nil {
		return err
	}
	var users []User
	err = db.Unscoped().Order("id").FindInBatches(&users, 200, func(batch *gorm.DB, _ int) error {
		for i := range users {
			if err := db.Transaction(func(tx *gorm.DB) error {
				project, err := ensureDefaultProjectTx(tx, &users[i])
				if err != nil {
					return err
				}
				return backfillUserProjectTx(tx, users[i], *project)
			}); err != nil {
				return fmt.Errorf("project migration user %d: %w", users[i].Id, err)
			}
		}
		return nil
	}).Error
	if err != nil {
		return err
	}
	if err := normalizeRetiredServiceKeyOwners(db); err != nil {
		return err
	}
	after, err := readProjectMigrationAudit(db)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(struct{ Before, After ProjectMigrationAudit }{before, after})
	if err != nil {
		return err
	}
	common.SysLog("project migration audit: " + string(evidence))
	return after.Validate()
}

// normalizeRetiredServiceKeyOwners normalizes all retired credentials, including
// already-owned rows whose history still has actor zero. Keep the retired flag
// so a failed separate-log migration can be retried on the next startup.
func normalizeRetiredServiceKeyOwners(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable("tokens") || !db.Migrator().HasColumn(&Token{}, "is_service") {
		return nil
	}
	type retiredServiceKeyRow struct {
		ID        int
		ProjectID int
		UserID    int
	}
	var rows []retiredServiceKeyRow
	// Read the table directly so a soft-deleted row is normalized too: its
	// spend and its resources stay attached to the same person as the live ones.
	if err := db.Table("tokens").Select("id", "project_id", "user_id").
		Where("is_service = ?", true).Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	owners, updated := map[int]int{}, int64(0)
	for _, row := range rows {
		if row.ProjectID <= 0 {
			continue
		}
		ownerID := row.UserID
		if ownerID <= 0 {
			ownerID = owners[row.ProjectID]
		}
		if ownerID <= 0 {
			var project Project
			if err := db.Select("id", "owner_user_id").First(&project, row.ProjectID).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				project.OwnerUserID = 0
			}
			ownerID = project.OwnerUserID
			owners[row.ProjectID] = ownerID
		}
		if ownerID <= 0 {
			continue
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			result := tx.Table("tokens").Where("id = ? AND user_id = ?", row.ID, 0).Update("user_id", ownerID)
			if result.Error != nil {
				return result.Error
			}
			updated += result.RowsAffected
			return reattributeRetiredServiceKeyRecords(tx, row.ID, row.ProjectID, ownerID)
		}); err != nil {
			return err
		}
	}
	if updated > 0 {
		common.SysLog(fmt.Sprintf("project migration: attributed %d ownerless service Key row(s) to their project owner", updated))
	}
	return nil
}

// reattributeRetiredServiceKeyRecords moves everything the Key already produced
// onto the owner the Key itself just received. Leaving these behind would be
// worse than doing nothing: the budget sums read the actor, so the owner's
// month would ignore the Key's own earlier charges and its pending holds and
// let the next request through past the cap, and the usage view and resources
// would belong to nobody. Every statement is scoped to this one Key inside its
// own project, and each is idempotent — the update makes its predicate stop
// matching.
func reattributeRetiredServiceKeyRecords(db *gorm.DB, tokenID, projectID, ownerID int) error {
	if db == nil || tokenID <= 0 || projectID <= 0 || ownerID <= 0 {
		return nil
	}
	// table, actor column, token column. A nullable token column still compares
	// equal, so files and media jobs need no special handling.
	for _, target := range []struct{ table, actor, token string }{
		{"project_spends", "actor_user_id", "token_id"},
		{"spend_budget_reservations", "actor_user_id", "token_id"},
		{"spend_budget_reservations", "user_id", "token_id"},
		{"settlement_outboxes", "actor_user_id", "token_id"},
		{"settlement_failures", "actor_user_id", "token_id"},
		{"files", "actor_user_id", "token_id"},
		{"media_jobs", "actor_user_id", "token_id"},
		{"logs", "user_id", "token_id"},
	} {
		if !db.Migrator().HasTable(target.table) {
			continue
		}
		where := fmt.Sprintf("project_id = ? AND %s = ? AND %s = ?", target.token, target.actor)
		if err := db.Table(target.table).Where(where, projectID, tokenID, 0).
			Update(target.actor, ownerID).Error; err != nil {
			return err
		}
	}
	return nil
}

type projectAttribution struct {
	ProjectID   int
	PayerUserID int
	ActorUserID int
}

func backfillUserProjectTx(tx *gorm.DB, user User, project Project) error {
	// Query the underlying table to include soft-deleted credentials as well:
	// their historical budgets/attribution must remain attached to this project.
	if err := tx.Table("tokens").Where("user_id = ? AND (project_id = 0 OR project_id IS NULL)", user.Id).Update("project_id", project.ID).Error; err != nil {
		return err
	}
	if err := tx.Table("top_ups").Where("user_id = ? AND (project_id = 0 OR project_id IS NULL)", user.Id).Select("project_id", "paid_by_user_id", "stripe_customer_id").Updates(&TopUp{ProjectID: project.ID, PaidByUserID: user.Id, StripeCustomerID: project.StripeCustomerID}).Error; err != nil {
		return err
	}
	if err := tx.Table("spend_budgets").Where("scope = ? AND user_id = ? AND (project_id = 0 OR project_id IS NULL)", SpendBudgetScopeUser, user.Id).Update("project_id", project.ID).Error; err != nil {
		return err
	}
	tokenIDs := tx.Table("tokens").Select("id").Where("project_id = ?", project.ID)
	if err := tx.Table("spend_budgets").Where("scope = ? AND token_id IN (?) AND (project_id = 0 OR project_id IS NULL)", SpendBudgetScopeToken, tokenIDs).Update("project_id", project.ID).Error; err != nil {
		return err
	}
	// A record emitted for a platform budget still belongs to the requesting
	// project; the budget row itself deliberately stays project_id=0.
	for _, table := range []string{"spend_budget_alerts", "spend_budget_reservations", "spend_budget_charges", "media_jobs", "files"} {
		if err := tx.Table(table).Where("user_id = ? AND (project_id = 0 OR project_id IS NULL)", user.Id).Updates(projectAttribution{ProjectID: project.ID, PayerUserID: user.Id, ActorUserID: user.Id}).Error; err != nil {
			return err
		}
	}
	for _, table := range []string{"settlement_outboxes", "settlement_failures"} {
		if !tx.Migrator().HasTable(table) {
			continue
		}
		if err := tx.Table(table).Where("user_id = ? AND (project_id = 0 OR project_id IS NULL)", user.Id).
			Select("project_id", "actor_user_id").
			Updates(&SettlementFailure{ProjectID: project.ID, ActorUserID: user.Id}).Error; err != nil {
			return err
		}
	}

	return nil
}

// Separate databases cannot share a transaction. Re-scan the retained retired
// flag and update only actorless logs, making this phase independently retryable.
func reattributeRetiredServiceKeyLogs(primary, logs *gorm.DB) error {
	if primary == nil || logs == nil {
		return fmt.Errorf("project log migration: database is not initialized")
	}
	if !primary.Migrator().HasColumn(&Token{}, "is_service") {
		return nil
	}
	var rows []struct {
		ID        int
		ProjectID int
		UserID    int
	}
	if err := primary.Table("tokens").Select("id", "project_id", "user_id").Where("is_service = ? AND user_id > 0 AND project_id > 0", true).Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := logs.Table("logs").Where("token_id = ? AND project_id = ? AND user_id = 0", row.ID, row.ProjectID).Update("user_id", row.UserID).Error; err != nil {
			return err
		}
	}
	return nil
}
