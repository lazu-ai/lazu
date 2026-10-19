package model

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
)

// OriginalLedgerSettlement preserves the immutable payer/key/budget periods
// of a refund. Redis covers SQL writer lag; permanent operations cover expiry.
func OriginalLedgerSettlement(ctx context.Context, requestID string) (ledger.Event, error) {
	var event ledger.Event
	if common.RDB != nil {
		row, err := common.RDB.HGetAll(ctx, ledger.Prefix+"request:"+requestID).Result()
		if err == nil && row["state"] == "settled" && row["input"] != "" {
			if err := json.Unmarshal([]byte(row["input"]), &event); err != nil {
				return event, err
			}
			if event.Kind == "settle" && event.RequestID == requestID {
				return event, nil
			}
		}
	}
	var operation RedisLedgerOperation
	if err := CoreDB().WithContext(ctx).Where("id = ?", ledgerOperationID(requestID, "settle")).First(&operation).Error; err != nil {
		return event, fmt.Errorf("original financial settlement unavailable: %w", err)
	}
	if err := json.Unmarshal([]byte(operation.Input), &event); err != nil {
		return event, err
	}
	if event.Kind != "settle" || event.RequestID != requestID {
		return event, fmt.Errorf("original financial settlement identity mismatch")
	}
	return event, nil
}
