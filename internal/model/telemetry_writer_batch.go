package model

import (
	"encoding/json"
	"errors"

	"gorm.io/gorm"
)

type relayWriterBatch struct {
	results     []relayResultEvent
	receipts    []TelemetryFinancialReceipt
	diagnostics []RequestDiagnosticsEvent
}

// Financial receipts and full relay facts are different idempotent records,
// but share a transaction when delivered together. Diagnostics for fresh logs
// join the INSERT; late diagnostics still use the expiry-aware batch UPDATE.
func persistRelayWriterBatch(batch relayWriterBatch) (map[string]bool, error) {
	db := TelemetryDB()
	if db == nil {
		return nil, errors.New("telemetry database unavailable")
	}
	merged := make(map[string]map[string]interface{})
	for _, patch := range batch.diagnostics {
		if patch.RequestID == "" || len(patch.Fields) == 0 {
			continue
		}
		if merged[patch.RequestID] == nil {
			merged[patch.RequestID] = make(map[string]interface{})
		}
		for field, value := range patch.Fields {
			merged[patch.RequestID][field] = value
		}
	}
	var committed []Log
	var found map[string]bool
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := persistFinancialTelemetryBatchTx(tx, batch.receipts); err != nil {
			return err
		}
		var err error
		committed, found, err = persistRelayResultBatchTx(tx, batch.results, merged)
		if err != nil {
			return err
		}
		late := make([]RequestDiagnosticsEvent, 0, len(merged))
		for requestID, fields := range merged {
			if !found[requestID] {
				late = append(late, RequestDiagnosticsEvent{RequestID: requestID, Fields: fields})
			}
		}
		updated, err := updateRequestLogDiagnosticsBatchTx(tx, late)
		if err != nil {
			return err
		}
		for requestID := range updated {
			found[requestID] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	projectCommittedTelemetry(committed)
	return found, nil
}

func (batch *relayWriterBatch) decode(kind, payload string) error {
	switch kind {
	case RelayEventTypeRelayResult:
		var event relayResultEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return err
		}
		batch.results = append(batch.results, event)
	case RelayEventTypeFinancialSettlement:
		var receipt TelemetryFinancialReceipt
		if err := json.Unmarshal([]byte(payload), &receipt); err != nil {
			return err
		}
		if receipt.EventID == "" || receipt.RequestID == "" {
			return errors.New("financial telemetry identity is required")
		}
		batch.receipts = append(batch.receipts, receipt)
	case RelayEventTypeRequestDiagnostics:
		var patch RequestDiagnosticsEvent
		if err := json.Unmarshal([]byte(payload), &patch); err != nil {
			return err
		}
		batch.diagnostics = append(batch.diagnostics, patch)
	default:
		return errors.New("unsupported batched relay event")
	}
	return nil
}
