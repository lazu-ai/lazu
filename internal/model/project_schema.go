package model

import (
	"fmt"

	"gorm.io/gorm"
)

func teamProjectSchemaModels() []interface{} {
	return []interface{}{&Project{}, &ProjectMember{}, &ProjectInvite{}, &BudgetRequest{}, &ProjectRemoval{}, &ProjectRemovalKey{}, &ProjectBudgetHistory{}, &ProjectNotification{}, &TokenCredential{}, &TokenRotation{}, &ProjectSpend{}, &ProjectDataMigration{}}
}
func validateTeamProjectSchema(db *gorm.DB, scope string) error {
	if scope == schemaVersionPrimary {
		for _, model := range teamProjectSchemaModels() {
			if !db.Migrator().HasTable(model) {
				return fmt.Errorf("project schema missing %T; run lazu-migrate", model)
			}
		}
		for _, column := range []struct {
			Model interface{}
			Name  string
		}{
			{&BudgetRequest{}, "approved_budget_microusd"}, {&Project{}, "billing_details"}, {&Project{}, "stripe_customer_started_at"}, {&ProjectRemoval{}, "undo_result"}, {&ProjectRemovalKey{}, "token_public_id"}, {&TopUp{}, "balance_after_microusd"}, {&TopUp{}, "public_id"}, {&TopUp{}, "stripe_price_id"}, {&TopUp{}, "stripe_project_name"}, {&TopUp{}, "stripe_checkout_url"}, {&ProjectDataMigration{}, "max_log_id"},
			{&SpendBudgetAlert{}, "project_id"}, {&SpendBudgetAlert{}, "payer_user_id"}, {&SpendBudgetAlert{}, "actor_user_id"}, {&SpendBudgetReservation{}, "project_id"}, {&SpendBudgetReservation{}, "payer_user_id"}, {&SpendBudgetReservation{}, "actor_user_id"}, {&SpendBudgetCharge{}, "project_id"}, {&SpendBudgetCharge{}, "payer_user_id"}, {&SpendBudgetCharge{}, "actor_user_id"},
			{&User{}, "verified_email"}, {&Token{}, "project_id"}, {&Token{}, "project_version"}, {&Token{}, "removal_operation_id"}, {&File{}, "project_id"}, {&MediaJob{}, "project_id"}, {&SpendBudget{}, "project_id"}, {&TopUp{}, "project_id"}, {&SettlementOutbox{}, "actor_user_id"}, {&SettlementFailure{}, "actor_user_id"},
		} {
			if !db.Migrator().HasColumn(column.Model, column.Name) {
				return fmt.Errorf("project schema missing %T.%s; run lazu-migrate", column.Model, column.Name)
			}
		}
	}
	if scope == schemaVersionLog {
		for _, column := range []string{"project_id", "payer_user_id", "rejection_reason"} {
			if !db.Migrator().HasColumn(&Log{}, column) {
				return fmt.Errorf("project log schema missing logs.%s; run lazu-migrate", column)
			}
		}
	}
	return nil
}
