package controller

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestStreamBacklogMetricsReportPendingLagAndDeadLetters(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	oldEnabled, oldRDB := common.RedisEnabled, common.RDB
	oldStream, oldGroup := common.RelayEventStream, common.RelayEventGroup
	common.RedisEnabled, common.RDB = true, client
	common.RelayEventStream, common.RelayEventGroup = "lazu:test:metrics-backlog", "lazu-test-backlog-workers"
	ctx := context.Background()
	cleanup := func() {
		client.Del(ctx, common.RelayEventStream, common.RelayEventStream+":dead")
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		common.RedisEnabled, common.RDB = oldEnabled, oldRDB
		common.RelayEventStream, common.RelayEventGroup = oldStream, oldGroup
	})

	if err := client.XGroupCreateMkStream(ctx, common.RelayEventStream, common.RelayEventGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		client.XAdd(ctx, &redis.XAddArgs{Stream: common.RelayEventStream, Values: map[string]interface{}{"k": "v"}})
	}
	// One delivered and left unacknowledged; two never delivered.
	if err := client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: common.RelayEventGroup, Consumer: "c", Streams: []string{common.RelayEventStream, ">"}, Count: 1}).Err(); err != nil {
		t.Fatal(err)
	}
	client.XAdd(ctx, &redis.XAddArgs{Stream: common.RelayEventStream + ":dead", Values: map[string]interface{}{"k": "v"}})

	var b strings.Builder
	writeStreamBacklogMetrics(&b)
	body := b.String()
	for _, want := range []string{
		`lazu_stream_length{stream="telemetry"} 3`,
		`lazu_stream_pending{stream="telemetry"} 1`,
		`lazu_stream_dead_letters{stream="telemetry"} 1`,
		`lazu_stream_length{stream="financial"}`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics lack %q:\n%s", want, body)
		}
	}
	// Redis 7 reports lag; older servers omit the series rather than guess.
	if strings.Contains(body, `lazu_stream_lag{stream="telemetry"}`) && !strings.Contains(body, `lazu_stream_lag{stream="telemetry"} 2`) {
		t.Fatalf("telemetry lag should be 2:\n%s", body)
	}
}
