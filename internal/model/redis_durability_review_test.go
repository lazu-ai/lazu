package model

import (
	"context"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestPR380RedisMemoryAndAOFAlerts(t *testing.T) {
	for _, test := range []struct {
		name, memory, persistence string
		memoryAlert, aofAlert     bool
	}{
		{"healthy", "used_memory:700\r\nmaxmemory:1000\r\n", "aof_enabled:1\r\naof_last_write_status:ok\r\naof_last_bgrewrite_status:ok\r\n", false, false},
		{"over70", "used_memory:701\r\nmaxmemory:1000\r\n", "aof_enabled:1\r\naof_last_write_status:ok\r\naof_last_bgrewrite_status:ok\r\n", true, false},
		{"writeFailed", "used_memory:800\r\nmaxmemory:1000\r\n", "aof_enabled:1\r\naof_last_write_status:err\r\naof_last_bgrewrite_status:ok\r\n", true, true},
		{"rewriteFailed", "used_memory:700\r\nmaxmemory:1000\r\n", "aof_enabled:1\r\naof_last_write_status:ok\r\naof_last_bgrewrite_status:err\r\n", false, true},
		{"disabled", "used_memory:700\r\nmaxmemory:1000\r\n", "aof_enabled:0\r\n", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			alerts := redisDurabilityAlerts(test.memory, test.persistence)
			if (alerts["redis-memory"] != "") != test.memoryAlert || (alerts["redis-aof"] != "") != test.aofAlert {
				t.Fatalf("alerts=%v", alerts)
			}
		})
	}
}

func TestPR380AOFAlertSurvivesRedisDedupWriteFailure(t *testing.T) {
	previous := common.RDB
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	_ = client.Close()
	common.RDB = client
	ledgerAlertNotifier.RLock()
	notifier := ledgerAlertNotifier.send
	ledgerAlertNotifier.RUnlock()
	t.Cleanup(func() {
		common.RDB = previous
		SetLedgerAlertNotifier(notifier)
		ledgerAlertFallback.Lock()
		delete(ledgerAlertFallback.until, "fixture-aof-write-failed")
		ledgerAlertFallback.Unlock()
	})
	calls := 0
	SetLedgerAlertNotifier(func(LedgerAlertNotice) error { calls++; return nil })
	notifyLedgerAlert(context.Background(), "fixture-aof-write-failed", "AOF write failed")
	notifyLedgerAlert(context.Background(), "fixture-aof-write-failed", "AOF write failed")
	if calls != 1 {
		t.Fatalf("failed Redis writes suppressed alert or lost throttle: calls=%d", calls)
	}
}
