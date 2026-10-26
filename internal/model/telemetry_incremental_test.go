package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/ledger"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func isolatedTelemetryStores(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	oldCore, oldTelemetry := coreDB, telemetryDB
	open := func(name string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s-%s?mode=memory&cache=shared", t.Name(), name)), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	core, telemetry := open("core"), open("telemetry")
	coreDB, telemetryDB = core, telemetry
	if err := core.AutoMigrate(&User{}, &Channel{}, &Ability{}, &TelemetryCleanupJob{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetry.AutoMigrate(telemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		coreDB, telemetryDB = oldCore, oldTelemetry
		for _, db := range []*gorm.DB{core, telemetry} {
			sql, _ := db.DB()
			_ = sql.Close()
		}
	})
	return core, telemetry
}

func TestFinancialEnvelopeIsIdempotentAndDoesNotCountUsage(t *testing.T) {
	_, db := isolatedTelemetryStores(t)
	event := telemetryFixture(common.GenerateRequestID())
	financial := ledger.Event{Input: ledger.Input{RequestID: event.ConsumeLog.RequestID, Amount: 9007199254740993, PayerID: 7, ProjectID: 11, TokenID: 9, OccurredAt: time.Now().UTC()}, OperationID: "settle:" + event.ConsumeLog.RequestID, Kind: "settle"}
	body, err := EncodeFinancialTelemetry(financial)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := handleRelayEvent(RelayEventTypeFinancialSettlement, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	var receipt TelemetryFinancialReceipt
	if err := db.First(&receipt).Error; err != nil {
		t.Fatal(err)
	}
	if receipt.AmountMicrousd != financial.Amount {
		t.Fatalf("financial envelope rounded amount: %d", receipt.AmountMicrousd)
	}
	var n int64
	if err := db.Model(&Log{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("financial envelope became a usage row")
	}
	if err := persistRelayResultBatch([]relayResultEvent{event, event}); err != nil {
		t.Fatal(err)
	}
	var counter ModelRequestCounter
	if err := db.First(&counter).Error; err != nil {
		t.Fatal(err)
	}
	if counter.TotalRequests != 1 {
		t.Fatal("financial envelope double counted requests")
	}
}
func telemetryFixture(request string) relayResultEvent {
	now := time.Now().UTC()
	user, channel, status := 7, 3, 200
	duration := 150
	row := Log{RequestID: request, CreatedAt: now.Unix(), Type: LogTypeConsume, UserId: user, ProjectID: 11, PayerUserID: 7, TokenId: 9, ModelName: "fixture", AmountMicrousd: billing.MicroUSD(123), PromptTokens: 4, CompletionTokens: 6, SessionID: "session-one", TokenName: "key", Username: "actor"}
	ensureConsumeLogPublicID(&row)
	log := newRelayConsumeLogEvent(&row)
	return relayResultEvent{ConsumeLog: &log, RoutingDecision: &RoutingDecision{RequestID: request, DecisionAt: now, IsPrimary: true, UserID: &user, ResolvedModel: "fixture", ChoseChannel: &channel, StrategyName: "baseline"}, RoutingOutcome: &RoutingOutcome{RequestID: request, OutcomeAt: now, UserID: &user, ChannelID: &channel, ModelName: "fixture", StatusCode: &status, TotalDurationMs: &duration, ChannelName: "provider"}, RoutingAttempts: []RoutingAttemptOutcome{{RequestID: request, AttemptIndex: 0, ChannelID: 2, OutcomeAt: now, StatusCode: 503}}}
}
func TestTelemetryBatchReplayIsAtomicAndCountersLiveOutsideCore(t *testing.T) {
	core, db := isolatedTelemetryStores(t)
	event := telemetryFixture(common.GenerateRequestID())
	for i := 0; i < 3; i++ {
		if err := persistRelayResultBatch([]relayResultEvent{event, event}); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []interface{}{&Log{}, &TelemetryEventReceipt{}, &RoutingAttemptOutcome{}} {
		var n int64
		if err := db.Model(row).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%T count=%d", row, n)
		}
	}
	var session AgentSession
	if err := db.First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.RequestCount != 1 || session.AmountMicrousd != 123 {
		t.Fatalf("session repeated: %+v", session)
	}
	var counter ModelRequestCounter
	if err := db.First(&counter).Error; err != nil {
		t.Fatal(err)
	}
	if counter.TotalRequests != 1 {
		t.Fatalf("model counter=%d", counter.TotalRequests)
	}
	var total DashboardUsageHourlyTotal
	if err := db.Where("scope_user_id = 0").First(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total.RequestCount != 1 || total.OutcomeCount != 1 || total.AmountMicrousd != 123 {
		t.Fatalf("hour repeated: %+v", total)
	}
	var daily DashboardUsageDailyDimension
	if err := db.Where("dimension_type = ?", "project").First(&daily).Error; err != nil {
		t.Fatal(err)
	}
	if daily.RequestCount != 1 || daily.AmountMicrousd != 123 {
		t.Fatalf("daily repeated: %+v", daily)
	}
	if core.Migrator().HasTable(&Log{}) || core.Migrator().HasTable(&ModelRequestCounter{}) {
		t.Fatal("telemetry schema leaked to core")
	}
}
func TestTelemetryBatchFailureRollsBackReceiptLogAndDerivedRows(t *testing.T) {
	_, db := isolatedTelemetryStores(t)
	event := telemetryFixture(common.GenerateRequestID())
	if err := db.Exec("CREATE TRIGGER fail_hourly BEFORE INSERT ON dashboard_usage_hourly_totals BEGIN SELECT RAISE(ABORT,'injected persistence failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := handleRelayResultEvent(event); err == nil {
		t.Fatal("expected injected failure")
	}
	for _, row := range []interface{}{&Log{}, &TelemetryEventReceipt{}, &AgentSession{}, &ModelRequestCounter{}} {
		var n int64
		if err := db.Model(row).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%T survived failed transaction: %d", row, n)
		}
	}
	if err := db.Exec("DROP TRIGGER fail_hourly").Error; err != nil {
		t.Fatal(err)
	}
	if err := handleRelayResultEvent(event); err != nil {
		t.Fatal(err)
	}
}
func TestTelemetryBatchAggregatesSeveralRequestsAndPreservesRetryEvidence(t *testing.T) {
	_, db := isolatedTelemetryStores(t)
	events := []relayResultEvent{telemetryFixture(common.GenerateRequestID()), telemetryFixture(common.GenerateRequestID())}
	if err := persistRelayResultBatch(events); err != nil {
		t.Fatal(err)
	}
	var total DashboardUsageHourlyTotal
	if err := db.Where("scope_user_id = 0").First(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total.RequestCount != 2 || total.AmountMicrousd != 246 {
		t.Fatalf("bad sum: %+v", total)
	}
	var n int64
	if err := db.Model(&RoutingAttemptOutcome{}).Where("status_code = ?", 503).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("superseded attempts=%d", n)
	}
	var counter ModelRequestCounter
	if err := db.First(&counter).Error; err != nil {
		t.Fatal(err)
	}
	if counter.TotalRequests != 2 {
		t.Fatalf("attempts counted as requests: %d", counter.TotalRequests)
	}
}
func TestCopyTelemetryHistoryIsResumableAcrossStores(t *testing.T) {
	source, target := isolatedTelemetryStores(t)
	if err := source.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	row := Log{UserId: 3, RequestID: common.GenerateRequestID(), CreatedAt: time.Now().Unix(), Type: LogTypeConsume, AmountMicrousd: 22}
	if err := source.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := CopyTelemetryHistory(context.Background(), source, target); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	if err := target.Model(&Log{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("copied twice: %d", n)
	}
	var copied Log
	if err := target.First(&copied).Error; err != nil {
		t.Fatal(err)
	}
	if copied.RequestTS != row.RequestTS || copied.AmountMicrousd != 22 {
		t.Fatalf("copy changed immutable event: %+v", copied)
	}
}
func TestRequestTimestampIsImmutableFromULID(t *testing.T) {
	id := common.GenerateRequestID()
	a, b := requestTimestamp(id, 100), requestTimestamp(id, 200)
	if a != b || a <= 200000 {
		t.Fatalf("timestamp derives from fallback: %d,%d", a, b)
	}
}
