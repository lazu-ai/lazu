package model

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// The durable intent precedes wallet mutation. Unique request IDs and a locked
// row let a worker resume after either wallet commit or log emission.
type DecisionGenerationRefund struct {
	ID        uint   `gorm:"primaryKey"`
	RequestID string `gorm:"size:64;uniqueIndex;not null"`
	Receipt   string `gorm:"type:text;not null"`
	Reason    string `gorm:"size:256"`
	Status    string `gorm:"size:16;index;not null;default:'open'"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func RecordDecisionGenerationRefund(receipt common.InternalChargeReceipt, reason string) error {
	if receipt.RequestID == "" || receipt.UserID <= 0 || receipt.TokenID <= 0 || receipt.ChargedMicrousd < 0 {
		return fmt.Errorf("invalid generation charge receipt")
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	row := DecisionGenerationRefund{RequestID: receipt.RequestID, Receipt: string(body), Reason: reason, Status: "open"}
	if err := CoreDB().Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	return ApplyDecisionGenerationRefund(receipt.RequestID)
}

func ApplyDecisionGenerationRefund(requestID string) error {
	var receipt common.InternalChargeReceipt
	var applied bool
	var tokenCacheKey string
	err := CoreDB().Transaction(func(tx *gorm.DB) error {
		var row DecisionGenerationRefund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&row).Error; err != nil {
			return err
		}
		if row.Status == "done" {
			return nil
		}
		if err := json.Unmarshal([]byte(row.Receipt), &receipt); err != nil {
			return err
		}
		if row.Status == "open" {
			if GatewayLedgerEnabled() {
				original, err := OriginalLedgerSettlement(context.Background(), requestID)
				if err != nil {
					return err
				}
				if original.PayerID != receipt.UserID || original.TokenID != receipt.TokenID || original.ProjectID != receipt.ProjectID || original.ActorID != receipt.ActorUserID {
					return fmt.Errorf("generation refund identity differs from original settlement")
				}
				receipt.ChargedMicrousd = original.Amount
				if original.Amount < 0 {
					return fmt.Errorf("invalid original generation charge")
				}
				if original.Amount > 0 {
					input := original.Input
					input.ReferenceRequestID = requestID
					if err := GatewayLedger().Refund(context.Background(), input, original.Amount, "decision_generation_refund:"+requestID); err != nil {
						return err
					}
				}
				body, err := json.Marshal(receipt)
				if err != nil {
					return err
				}
				// The financial writer adds reversal rows in the original periods.
				// Original charges and daily summaries are never zeroed here.
				return tx.Model(&row).Updates(map[string]interface{}{"status": "applied", "receipt": string(body)}).Error
			}
			// Wait for any accepted settlement to complete before undoing it.
			if tx.Migrator().HasTable(&SettlementOutbox{}) {
				var pending int64
				if err := tx.Model(&SettlementOutbox{}).Where("request_id = ? AND status <> ?", requestID, SettlementFailureStatusResolved).Count(&pending).Error; err != nil {
					return err
				}
				if pending > 0 {
					return fmt.Errorf("generation settlement is still pending")
				}
			}
			if tx.Migrator().HasTable(&SettlementFailure{}) {
				var failures []SettlementFailure
				if err := tx.Where("request_id = ?", requestID).Order("id ASC").Find(&failures).Error; err != nil {
					return err
				}
				for _, failure := range failures {
					if failure.Status != SettlementFailureStatusResolved {
						return fmt.Errorf("generation settlement failure is still pending")
					}
					receipt.ChargedMicrousd = (failure.PreConsumed + failure.QuotaDelta).Int64()
				}
				if receipt.ChargedMicrousd < 0 {
					return fmt.Errorf("invalid reconciled generation charge")
				}
			}
			amount := billing.MicroUSD(receipt.ChargedMicrousd)
			metadata := map[string]interface{}{"generation_request_id": requestID, "refund_of_request_id": requestID, "reason": row.Reason, "project_id": receipt.ProjectID, "actor_user_id": receipt.ActorUserID}
			input := AdjustRelayQuotaWithLedgerInput{AcceptedSettlement: true, UserID: receipt.UserID, TokenID: receipt.TokenID, TokenUnlimited: receipt.Unlimited, QuotaDelta: -amount, RefType: "decision_generation_refund", RefID: requestID, PricingVersion: receipt.PricingVersion, Metadata: metadata}
			var err error
			tokenCacheKey, err = adjustRelayQuotaWithLedgerTx(tx, input, WalletLedgerKindRefund, metadata)
			if err != nil {
				return err
			}
			// Reverse the original accounting rows, keeping their original period.
			if tx.Migrator().HasTable(&SpendBudgetCharge{}) {
				if err := tx.Model(&SpendBudgetCharge{}).Where("request_id = ? AND project_id = ? AND token_id = ?", requestID, receipt.ProjectID, receipt.TokenID).Update("amount_microusd", 0).Error; err != nil {
					return err
				}
			}
			if tx.Migrator().HasTable(&ProjectSpend{}) {
				if err := tx.Model(&ProjectSpend{}).Where("request_id = ? AND project_id = ? AND token_id = ?", requestID, receipt.ProjectID, receipt.TokenID).Update("amount_microusd", 0).Error; err != nil {
					return err
				}
			}
			updatedReceipt, err := json.Marshal(receipt)
			if err != nil {
				return err
			}
			if err := tx.Model(&row).Updates(map[string]interface{}{"status": "applied", "receipt": string(updatedReceipt)}).Error; err != nil {
				return err
			}
			applied = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if applied {
		updateRelayQuotaCachesAfterCommit(receipt.UserID, tokenCacheKey, -billing.MicroUSD(receipt.ChargedMicrousd), nil, nil)
	}
	return recordDecisionRefundLog(requestID)
}

func recordDecisionRefundLog(requestID string) error {
	if TelemetryDB() == nil {
		return fmt.Errorf("refund log database unavailable")
	}
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		var row DecisionGenerationRefund
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", requestID).First(&row).Error; err != nil {
			return err
		}
		if row.Status == "done" {
			return nil
		}
		if row.Status != "applied" {
			return fmt.Errorf("refund has not been applied")
		}
		var receipt common.InternalChargeReceipt
		if err := json.Unmarshal([]byte(row.Receipt), &receipt); err != nil {
			return err
		}
		logDB := TelemetryDB()
		if TelemetryDB() == CoreDB() {
			logDB = tx
		}
		if GatewayLedgerEnabled() {
			if !relayEventQueueAvailable() {
				return fmt.Errorf("refund telemetry queue unavailable")
			}
			other, _ := json.Marshal(map[string]interface{}{"generation_request_id": requestID, "refund_of_request_id": requestID, "refund_reason": row.Reason, "request_kind": "decision_question_generation_refund"})
			log := Log{UserId: receipt.ActorUserID, ProjectID: receipt.ProjectID, PayerUserID: receipt.UserID, RequestID: requestID, Type: LogTypeRefund, CreatedAt: time.Now().Unix(), TokenId: receipt.TokenID, ChannelId: receipt.ChannelID, ModelName: receipt.Model, AmountMicrousd: -billing.MicroUSD(receipt.ChargedMicrousd), PricingVersion: receipt.PricingVersion, Content: row.Reason, Other: string(other)}
			if err := PublishRelayEvent(RelayEventTypeConsumeLog, newRelayConsumeLogEvent(&log)); err != nil {
				return err
			}
			return tx.Model(&row).Update("status", "done").Error
		}
		var count int64
		if err := logDB.Model(&Log{}).Where("request_id = ? AND type = ?", requestID, LogTypeRefund).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			other, _ := json.Marshal(map[string]interface{}{"generation_request_id": requestID, "refund_of_request_id": requestID, "refund_reason": row.Reason, "request_kind": "decision_question_generation_refund"})
			log := Log{UserId: receipt.ActorUserID, ProjectID: receipt.ProjectID, PayerUserID: receipt.UserID, RequestID: requestID, Type: LogTypeRefund, CreatedAt: time.Now().Unix(), TokenId: receipt.TokenID, ChannelId: receipt.ChannelID, ModelName: receipt.Model, AmountMicrousd: -billing.MicroUSD(receipt.ChargedMicrousd), PricingVersion: receipt.PricingVersion, Content: row.Reason, Other: string(other)}
			if err := logDB.Create(&log).Error; err != nil {
				return err
			}
		}
		return tx.Model(&row).Update("status", "done").Error
	})
}

func ReconcileDecisionGenerationRefunds() error {
	if CoreDB() == nil || !CoreDB().Migrator().HasTable(&DecisionGenerationRefund{}) {
		return nil
	}
	var rows []DecisionGenerationRefund
	if err := CoreDB().Where("status <> ?", "done").Order("id ASC").Limit(50).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := ApplyDecisionGenerationRefund(row.RequestID); err != nil {
			common.SysError("decision refund retry failed: " + err.Error())
		}
	}
	return nil
}
