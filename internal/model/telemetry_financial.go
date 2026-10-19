package model

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/lazu-ai/lazu/internal/ledger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TelemetryFinancialReceipt records the minimal envelope atomically published
// by financial settlement. It never increments usage/session counters; the
// later relay_result owns the full model, routing and token measurements.
type TelemetryFinancialReceipt struct {
	EventID        string    `json:"event_id" gorm:"primaryKey;size:128"`
	RequestID      string    `json:"request_id" gorm:"size:64;index"`
	RequestTS      int64     `json:"request_ts" gorm:"index"`
	Kind           string    `json:"kind" gorm:"size:32"`
	PayerID        int       `json:"payer_id"`
	ActorID        int       `json:"actor_id"`
	ProjectID      int       `json:"project_id"`
	TokenID        int       `json:"token_id"`
	AmountMicrousd int64     `json:"amount_microusd"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func EncodeFinancialTelemetry(event ledger.Event) (string, error) {
	receipt := TelemetryFinancialReceipt{EventID: event.OperationID, RequestID: event.RequestID, RequestTS: requestTimestamp(event.RequestID, event.OccurredAt.Unix()), Kind: event.Kind, PayerID: event.PayerID, ActorID: event.ActorID, ProjectID: event.ProjectID, TokenID: event.TokenID, AmountMicrousd: event.Amount, OccurredAt: event.OccurredAt}
	body, err := json.Marshal(receipt)
	return string(body), err
}

func persistFinancialTelemetry(receipt TelemetryFinancialReceipt) error {
	if receipt.EventID == "" || receipt.RequestID == "" {
		return errors.New("financial telemetry identity is required")
	}
	return TelemetryDB().Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error
}

func persistFinancialTelemetryBatch(receipts []TelemetryFinancialReceipt) error {
	return persistFinancialTelemetryBatchTx(TelemetryDB(), receipts)
}

func persistFinancialTelemetryBatchTx(tx *gorm.DB, receipts []TelemetryFinancialReceipt) error {
	if len(receipts) == 0 {
		return nil
	}
	rows := map[string]*TelemetryFinancialReceipt{}
	for i := range receipts {
		receipt := receipts[i]
		if receipt.EventID == "" || receipt.RequestID == "" {
			return errors.New("financial telemetry identity is required")
		}
		rows[receipt.EventID] = &receipt
	}
	batch := sortedBatch(rows)
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&batch, 500).Error
}
