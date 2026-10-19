package common

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func TestRedisDispatchMetricsSeparateRequiredAndBackground(t *testing.T) {
	previous := HotPathMetricsEnabled
	HotPathMetricsEnabled = true
	defer func() { HotPathMetricsEnabled = previous }()
	before := SnapshotHotPathMetrics().Counters
	hook := redisDispatchMetricsHook{}
	request := WithRequestRedisMetrics(context.Background())
	_, _ = hook.BeforeProcess(request, nil)
	_, _ = hook.BeforeProcess(context.Background(), nil)
	_, _ = hook.BeforeProcessPipeline(request, []redis.Cmder{redis.NewCmd(request, "GET", "a"), redis.NewCmd(request, "GET", "b")})
	_, _ = hook.BeforeProcessPipeline(request, nil)
	after := SnapshotHotPathMetrics().Counters
	if after["redis.client_dispatches"]-before["redis.client_dispatches"] != 3 || after["redis.request_dispatches"]-before["redis.request_dispatches"] != 2 {
		t.Fatalf("pipelines/background were counted as request round trips: before=%v after=%v", before, after)
	}
}

func TestRelayTimingSeparatesProviderAndPostResponseWork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var fields map[string]interface{}
	router.Use(TrackRelayTiming(func(_ *gin.Context, values map[string]interface{}) { fields = values }))
	router.POST("/v1/responses", func(c *gin.Context) {
		time.Sleep(5 * time.Millisecond)
		timing := EnsureRelayTiming(c)
		timing.StartUpstream()
		time.Sleep(15 * time.Millisecond)
		timing.FirstUpstreamByte()
		_, _ = c.Writer.Write([]byte("data: done\n\n"))
		c.Writer.Flush()
		time.Sleep(10 * time.Millisecond)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	for key, min := range map[string]float64{"gateway_pre_ms": 4, "upstream_ttfb_ms": 14, "gateway_post_ms": 9} {
		value, ok := fields[key].(float64)
		if !ok || value < min {
			t.Fatalf("%s got %v, minimum %g", key, fields[key], min)
		}
	}
}

func TestRelayTimingRetainsSubMillisecondPrecision(t *testing.T) {
	start := time.Unix(1, 0)
	timing := &RelayTiming{started: start, outbound: start.Add(750 * time.Microsecond), firstByte: start.Add(2250 * time.Microsecond), lastWrite: start.Add(3 * time.Millisecond), ended: start.Add(3250 * time.Microsecond)}
	fields := timing.Fields()
	if fields["gateway_pre_ms"] != .75 || fields["upstream_ttfb_ms"] != 1.5 || fields["gateway_post_ms"] != .25 {
		t.Fatalf("timing was rounded before aggregation: %v", fields)
	}
}
func TestRelayTimingDoesNotInventUpstreamTimeForRejection(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	timing := EnsureRelayTiming(c)
	timing.End()
	if len(timing.Fields()) != 0 {
		t.Fatal("rejected request has invented provider timing")
	}
}

func TestRelayTimingRetryDoesNotReclassifyProviderWaitAsPreparation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	timing := EnsureRelayTiming(c)
	timing.StartUpstream()
	before := timing.Fields()["gateway_pre_ms"]
	time.Sleep(10 * time.Millisecond)
	timing.FirstUpstreamByte()
	timing.StartUpstream()
	if timing.Fields()["gateway_pre_ms"] != before {
		t.Fatal("retry included earlier provider wait in gateway_pre_ms")
	}
	if _, ok := timing.Fields()["upstream_ttfb_ms"]; !ok {
		t.Fatal("retry erased observed provider TTFB")
	}
}

func TestRelayPhaseDiagnosticsKeepPrecisionAndDisabledPath(t *testing.T) {
	previous := HotPathMetricsEnabled
	defer func() { HotPathMetricsEnabled = previous }()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	HotPathMetricsEnabled = false
	StartRelayPhase(c, RelayPhaseBilling).Finish()
	if _, exists := c.Get(relayTimingKey); exists {
		t.Fatal("disabled phase diagnostics allocated a request clock")
	}
	HotPathMetricsEnabled = true
	timing := EnsureRelayTiming(c)
	timing.AddPhase(RelayPhaseSettleEval, 125*time.Microsecond)
	timing.AddPhase(RelayPhaseSettleEval, 375*time.Microsecond)
	timing.AddPhase(RelayPhaseCapture, -time.Millisecond)
	if got := timing.Fields()[RelayPhaseSettleEval]; got != .5 {
		t.Fatalf("lost summed dispatch precision: %v", got)
	}
	if _, exists := timing.Fields()[RelayPhaseCapture]; exists {
		t.Fatal("negative phase duration became a fact")
	}
}

func TestRedisDispatchObserverIncludesClientWaitWithoutChangingCounts(t *testing.T) {
	previous := HotPathMetricsEnabled
	HotPathMetricsEnabled = true
	defer func() { HotPathMetricsEnabled = previous }()
	before := SnapshotHotPathMetrics().Counters
	var elapsed time.Duration
	ctx := WithRedisDispatchObserver(WithRequestRedisMetrics(context.Background()), func(duration time.Duration) { elapsed = duration })
	hook := redisDispatchMetricsHook{}
	ctx, _ = hook.BeforeProcess(ctx, nil)
	// Stand in for connection-pool/network wait inside the Redis client.
	ctx = context.WithValue(ctx, redisDispatchStartedKey{}, time.Now().Add(-5*time.Millisecond))
	_ = hook.AfterProcess(ctx, nil)
	if elapsed < 5*time.Millisecond {
		t.Fatalf("dispatch observer omitted client wait: %s", elapsed)
	}
	elapsed = 0
	ctx, _ = hook.BeforeProcessPipeline(ctx, []redis.Cmder{redis.NewCmd(ctx, "GET", "fixture")})
	ctx = context.WithValue(ctx, redisDispatchStartedKey{}, time.Now().Add(-5*time.Millisecond))
	_ = hook.AfterProcessPipeline(ctx, nil)
	if elapsed < 5*time.Millisecond {
		t.Fatalf("pipeline observer omitted client wait: %s", elapsed)
	}
	after := SnapshotHotPathMetrics().Counters
	if after["redis.request_dispatches"]-before["redis.request_dispatches"] != 2 || after["redis.client_dispatches"]-before["redis.client_dispatches"] != 2 {
		t.Fatal("timed Redis call changed dispatch accounting")
	}
}
