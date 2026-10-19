package router

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
)

// The HTTP producer must have returned (or its test server closed) before a
// fixture restores globals. Idle process-wide workers can then be reused by
// the next fixture without a pending task observing its database connection.
func drainRelayDiagnosticFixture(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := service.DrainRelayDiagnosticWork(ctx); err != nil {
		t.Fatalf("relay diagnostics did not drain before fixture store change: %v", err)
	}
}

func TestRelayDiagnosticsDrainPreservesFactsAcrossFixtureStores(t *testing.T) {
	drainRelayDiagnosticFixture(t)
	previousCounting, previousNonStream := constant.GetMediaToken, constant.GetMediaTokenNotStream
	constant.GetMediaToken, constant.GetMediaTokenNotStream = true, true
	t.Cleanup(func() {
		drainRelayDiagnosticFixture(t)
		constant.GetMediaToken, constant.GetMediaTokenNotStream = previousCounting, previousNonStream
	})
	for _, name := range []string{"first_store", "next_store"} {
		t.Run(name, func(t *testing.T) {
			cleanup := setupModelCatalogRouteTestDB(t)
			defer cleanup()
			previousTelemetry := model.ConfiguredTelemetryDB()
			model.SetTelemetryDB(model.CoreDB())
			defer func() {
				drainRelayDiagnosticFixture(t)
				model.SetTelemetryDB(previousTelemetry)
			}()
			if err := model.CoreDB().AutoMigrate(&model.Log{}); err != nil {
				t.Fatal(err)
			}
			// SQLite's fixture uses one writer connection for the two concurrent
			// diagnostic workers' read-modify-write transactions.
			sqlDB, err := model.CoreDB().DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			requestID := common.GenerateRequestID()
			row := model.Log{RequestID: requestID, Type: model.LogTypeConsume, AmountMicrousd: 123, PromptTokens: 800, Other: "{}"}
			if err := model.CoreDB().Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(common.RequestIdKey, requestID)
			c.Set("protocol_token_shadow_meta", &types.TokenCountMeta{CombineText: "hello world", MessagesCount: 1})
			c.Set("protocol_token_shadow_model", "gpt-4o")
			c.Set("protocol_token_shadow_format", types.RelayFormatOpenAI)
			common.SetContextKey(c, constant.ContextKeyProtocolAdmissionTokens, 800)
			service.EnqueueProtocolTokenShadow(c)
			model.MarkRequestLogTarget(c, requestID)
			service.RecordRelayTimingDiagnostics(c, map[string]interface{}{"gateway_post_ms": 17})
			drainRelayDiagnosticFixture(t)
			var stored model.Log
			if err := model.CoreDB().Where("request_id = ?", requestID).First(&stored).Error; err != nil {
				t.Fatal(err)
			}
			var fields map[string]interface{}
			if err := json.Unmarshal([]byte(stored.Other), &fields); err != nil {
				t.Fatal(err)
			}
			local, ok := fields["protocol_local_prompt_tokens"].(float64)
			if !ok || local <= 0 || fields["protocol_admission_token_delta"] != local-800 || fields["gateway_post_ms"] != float64(17) {
				t.Fatalf("drain lost a queued shadow or timing fact: %+v", fields)
			}
			if stored.AmountMicrousd != 123 || stored.PromptTokens != 800 {
				t.Fatal("diagnostic drain changed billing facts")
			}
		})
	}
}
