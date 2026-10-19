package model

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
)

func withRelayResultQueueTestFlags(t *testing.T) {
	t.Helper()
	previousQueue := common.RelayEventQueueEnabled
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() {
		_ = client.Close()
		common.RelayEventQueueEnabled = previousQueue
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	common.RelayEventQueueEnabled = true
	common.RedisEnabled = true
	common.RDB = client
}

func newRelayResultQueueTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	return c
}

func TestQueueConsumeLogForRelayResultRequiresFlushMarker(t *testing.T) {
	withRelayResultQueueTestFlags(t)
	c := newRelayResultQueueTestContext()
	log := &Log{RequestID: "req-1", CreatedAt: common.GetTimestamp()}

	if queueConsumeLogForRelayResult(c, log) {
		t.Fatal("consume log should not queue unless the distributor marked a later flush")
	}
	if _, ok := c.Get(relayResultConsumeLogContextKey); ok {
		t.Fatal("consume log context should remain unset without the flush marker")
	}

	MarkRelayResultFlushExpected(c)
	if !queueConsumeLogForRelayResult(c, log) {
		t.Fatal("consume log should queue after the distributor marks a later flush")
	}
	value, ok := c.Get(relayResultConsumeLogContextKey)
	if !ok {
		t.Fatal("expected queued consume log on context")
	}
	event, ok := value.(relayConsumeLogEvent)
	if !ok {
		t.Fatalf("expected relayConsumeLogEvent, got %T", value)
	}
	if event.RequestID != "req-1" || event.PublicID == "" {
		t.Fatalf("queued consume log lost required fields: %+v", event)
	}
}

func TestFlushRelayResultEventClearsFlushMarker(t *testing.T) {
	withRelayResultQueueTestFlags(t)
	c := newRelayResultQueueTestContext()
	log := &Log{RequestID: "req-2", CreatedAt: common.GetTimestamp()}

	MarkRelayResultFlushExpected(c)
	FlushRelayResultEvent(c, nil)

	if queueConsumeLogForRelayResult(c, log) {
		t.Fatal("consume log should not queue after flush clears the marker")
	}
}

func TestProjectConsumeLogInheritsTrustedActorAndBillingSnapshot(t *testing.T) {
	withRelayResultQueueTestFlags(t)
	oldEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = oldEnabled })
	for _, actor := range []int{20, 0} {
		c := newRelayResultQueueTestContext()
		c.Set("id", actor)
		common.SetContextKey(c, constant.ContextKeyProjectID, 10)
		common.SetContextKey(c, constant.ContextKeyPayerUserID, 30)
		common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{})
		MarkRelayResultFlushExpected(c)
		RecordConsumeLog(c, 999, RecordConsumeLogParams{TokenId: 40, AmountMicrousd: 12, Other: map[string]interface{}{"provider_cost_usd": 0}})
		value, exists := c.Get(relayResultConsumeLogContextKey)
		if !exists {
			t.Fatal("log was not queued")
		}
		event := value.(relayConsumeLogEvent)
		if event.ProjectID != 10 || event.PayerUserID != 30 || event.UserID != actor {
			t.Fatalf("log lost shared attribution: %+v", event)
		}
	}
}

func TestConsumeLogRecordsTheAddressTheRequestWasSentTo(t *testing.T) {
	withRelayResultQueueTestFlags(t)
	oldEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = oldEnabled })
	c := newRelayResultQueueTestContext()
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request.Host = "API.Example.com:443"
	c.Request.Header.Set("User-Agent", "pi (darwin 25.2.0; arm64)")
	common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{})
	common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now().Add(-1500*time.Millisecond))
	MarkRelayResultFlushExpected(c)
	RecordConsumeLog(c, 1, RecordConsumeLogParams{TokenId: 2})
	value, exists := c.Get(relayResultConsumeLogContextKey)
	if !exists {
		t.Fatal("log was not queued")
	}
	event := value.(relayConsumeLogEvent)
	if event.RequestHost != "api.example.com" {
		t.Fatalf("request host = %q, want api.example.com", event.RequestHost)
	}
	if row := event.toLog(); row.RequestHost != "api.example.com" {
		t.Fatalf("row request host = %q after the event round trip", row.RequestHost)
	}
	if !strings.Contains(event.Other, `"duration_ms":15`) {
		t.Fatalf("success row lacks a millisecond duration: %s", event.Other)
	}
	if !strings.Contains(event.Other, `"user_agent":"pi (darwin 25.2.0; arm64)"`) || strings.Contains(event.Other, "client_cancelled") {
		t.Fatalf("request facts wrong: %s", event.Other)
	}
	if RequestHostOf(nil) != "" {
		t.Fatal("no request means no host")
	}
}

func TestRequestFactsMarkAClientThatWentAway(t *testing.T) {
	c := newRelayResultQueueTestContext()
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	cancel()
	other := map[string]interface{}{}
	stampRequestFacts(c, other)
	if other["client_cancelled"] != true {
		t.Fatalf("cancelled request not marked: %v", other)
	}
}
