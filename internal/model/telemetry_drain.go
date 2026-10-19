package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

// VerifyRelayEventsDrained is a maintenance check, never a request-path read.
// Writers must be stopped before calling it so the observed zero stays zero.
func VerifyRelayEventsDrained(ctx context.Context) error {
	if common.RDB == nil {
		return errors.New("Redis is required to verify the telemetry drain")
	}
	queued, err := common.RDB.XLen(ctx, common.RelayEventStream).Result()
	if err != nil {
		return err
	}
	pending, err := common.RDB.XPending(ctx, common.RelayEventStream, common.RelayEventGroup).Result()
	if err != nil && !errors.Is(err, redis.Nil) && !strings.Contains(err.Error(), "NOGROUP") {
		return err
	}
	var pendingCount int64
	if pending != nil {
		pendingCount = pending.Count
	}
	dead, err := common.RDB.XLen(ctx, common.RelayEventStream+":dead").Result()
	if err != nil {
		return err
	}
	if queued > 0 || pendingCount > 0 || dead > 0 {
		return fmt.Errorf("telemetry not drained: %d queued, %d pending, %d dead-letter events; persist or resolve them before rollback", queued, pendingCount, dead)
	}
	return nil
}
