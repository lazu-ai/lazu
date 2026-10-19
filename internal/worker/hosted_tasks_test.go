package worker

import (
	"context"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestClaimHostedTaskLeaseRequiresRedis(t *testing.T) {
	previousEnabled := common.RedisEnabled
	previousClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousEnabled
		common.RDB = previousClient
	})

	acquired, err := claimHostedTaskLease(context.Background(), "test", time.Minute)
	if err == nil {
		t.Fatal("expected hosted task lease to require Redis")
	}
	if acquired {
		t.Fatal("expected lease acquisition to fail closed")
	}
}
