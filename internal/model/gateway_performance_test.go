package model

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestGatewayPerformancePreservesUnknownAndInterpolatesPercentiles(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "timing.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previous := ConfiguredTelemetryDB()
	SetTelemetryDB(db)
	t.Cleanup(func() { SetTelemetryDB(previous); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, facts := range []map[string]interface{}{
		{"gateway_pre_ms": 10, "gateway_post_ms": 0},
		{"gateway_pre_ms": 30},
		{},
	} {
		raw, _ := json.Marshal(facts)
		row := &Log{Id: i + 1, RequestTS: day.Add(time.Duration(i) * time.Second).UnixMilli(), Other: string(raw)}
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&Log{Id: 4, RequestTS: day.AddDate(0, 0, 1).UnixMilli(), Other: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	days, err := GatewayPerformanceDays(context.Background(), day, day.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0].Samples != 2 || days[0].PostSamples != 1 || *days[0].PreP50 != 20 || *days[0].PreP95 != 29 || *days[0].PostP50 != 0 {
		t.Fatalf("unexpected observed percentiles: %+v", days)
	}
	if days[1].PreP50 != nil || days[1].PostP95 != nil {
		t.Fatal("missing instrumentation became zero")
	}
}

func TestGatewayPreparationRegressionRequiresBothThresholdsAndSevenDays(t *testing.T) {
	value := func(n float64) *float64 { return &n }
	days := make([]GatewayTimingDay, 0, 8)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		days = append(days, GatewayTimingDay{Day: start.AddDate(0, 0, i).Format("2006-01-02"), Samples: 1, PreP50: value(20)})
	}
	days = append(days, GatewayTimingDay{Day: "2026-10-08", Samples: 1, PreP50: value(31)})
	current, baseline, breach := GatewayPreparationRegression(days, "2026-10-08")
	if !breach || current != 31 || baseline != 20 {
		t.Fatalf("current=%v baseline=%v breach=%v", current, baseline, breach)
	}
	days[7].PreP50 = value(30)
	if _, _, breach := GatewayPreparationRegression(days, "2026-10-08"); breach {
		t.Fatal("equal to 50% threshold alerted")
	}
	days[7].PreP50 = value(19)
	if _, _, breach := GatewayPreparationRegression(days, "2026-10-08"); breach {
		t.Fatal("sub-20ms day alerted")
	}
	days[7].PreP50 = value(40)
	if _, _, breach := GatewayPreparationRegression(days[1:], "2026-10-08"); breach {
		t.Fatal("incomplete baseline alerted")
	}
}

func TestRequestDiagnosticEventMergesWithoutChangingAccounting(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open(filepath.Join(t.TempDir(), "diagnostics.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previous := ConfiguredTelemetryDB()
	SetTelemetryDB(db)
	t.Cleanup(func() { SetTelemetryDB(previous); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := db.AutoMigrate(&Log{}); err != nil {
		t.Fatal(err)
	}
	row := &Log{Id: 1, Type: LogTypeConsume, RequestID: "fixture-request", RequestTS: 1234, AmountMicrousd: 5678, PromptTokens: 90, Other: `{"usage_source":"provider"}`}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	event, _ := json.Marshal(RequestDiagnosticsEvent{RequestID: row.RequestID, Fields: map[string]interface{}{"gateway_pre_ms": 12}})
	for i := 0; i < 2; i++ {
		if err := handleRelayEvent(RelayEventTypeRequestDiagnostics, event); err != nil {
			t.Fatal(err)
		}
	}
	var stored Log
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	_ = json.Unmarshal([]byte(stored.Other), &fields)
	if stored.AmountMicrousd != row.AmountMicrousd || stored.PromptTokens != row.PromptTokens || fields["usage_source"] != "provider" || fields["gateway_pre_ms"] != float64(12) {
		t.Fatalf("accounting changed or fields lost: %+v", stored)
	}
	missing, _ := json.Marshal(RequestDiagnosticsEvent{RequestID: "missing", Fields: map[string]interface{}{"gateway_post_ms": 1}})
	if err := handleRelayEvent(RelayEventTypeRequestDiagnostics, missing); err != gorm.ErrRecordNotFound {
		t.Fatalf("missing event should retry, got %v", err)
	}
	found, err := UpdateRequestLogDiagnosticsBatch([]RequestDiagnosticsEvent{
		{RequestID: row.RequestID, Fields: map[string]interface{}{"gateway_post_ms": 3}},
		{RequestID: row.RequestID, Fields: map[string]interface{}{"token_shadow_match": true}},
		{RequestID: "missing", Fields: map[string]interface{}{"gateway_post_ms": 4}},
	})
	if err != nil || !found[row.RequestID] || found["missing"] {
		t.Fatalf("wrong batch retry selection: found=%v err=%v", found, err)
	}
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(stored.Other), &fields)
	if fields["gateway_post_ms"] != float64(3) || fields["token_shadow_match"] != true || fields["usage_source"] != "provider" || stored.AmountMicrousd != row.AmountMicrousd {
		t.Fatalf("batch lost merged fields or changed accounting: %+v", stored)
	}
	if err := db.Model(&Log{}).Where("id = ?", row.Id).Updates(map[string]interface{}{"type": LogTypeUnknown, "other": "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	found, err = UpdateRequestLogDiagnosticsBatch([]RequestDiagnosticsEvent{{RequestID: row.RequestID, Fields: map[string]interface{}{"token_shadow_text": "must stay expired"}}})
	if err != nil || !found[row.RequestID] {
		t.Fatalf("expired diagnostic event must be acknowledged: %v %v", found, err)
	}
	if err := db.First(&stored).Error; err != nil || stored.Other != "{}" {
		t.Fatalf("expired diagnostics resurrected: %q %v", stored.Other, err)
	}
}

func TestPostgresRequestDiagnosticBatchMergesAndPrunes(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	requestID := common.GenerateRequestID()
	requestTS := requestTimestamp(requestID, 0)
	row := Log{Type: LogTypeConsume, RequestID: requestID, RequestTS: requestTS, CreatedAt: time.Now().Unix(), Other: `{"usage_source":"provider"}`, AmountMicrousd: 12345}
	ensureConsumeLogPublicID(&row)
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	// A mismatched timestamp must never let a reused request-id touch another
	// partition identity, even if legacy/imported data contains such a row.
	wrong := Log{RequestID: requestID, RequestTS: requestTS + 1, CreatedAt: time.Now().Unix(), Other: `{}`}
	ensureConsumeLogPublicID(&wrong)
	if err := db.Create(&wrong).Error; err != nil {
		t.Fatal(err)
	}
	found, err := UpdateRequestLogDiagnosticsBatch([]RequestDiagnosticsEvent{
		{RequestID: requestID, Fields: map[string]interface{}{"gateway_pre_ms": 5}},
		{RequestID: requestID, Fields: map[string]interface{}{"gateway_post_ms": 2}},
		{RequestID: common.GenerateRequestID(), Fields: map[string]interface{}{"token_shadow_match": true}},
	})
	if err != nil || len(found) != 1 || !found[requestID] {
		t.Fatalf("unexpected updated identities: %v %v", found, err)
	}
	var stored Log
	if err := db.Where("id = ? AND request_ts = ?", row.Id, row.RequestTS).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	var fields map[string]interface{}
	_ = json.Unmarshal([]byte(stored.Other), &fields)
	if fields["gateway_pre_ms"] != float64(5) || fields["gateway_post_ms"] != float64(2) || fields["usage_source"] != "provider" || stored.AmountMicrousd != row.AmountMicrousd {
		t.Fatalf("batch fields/accounting incorrect: %+v", stored)
	}
	stored = Log{}
	if err := db.Where("id = ? AND request_ts = ?", wrong.Id, wrong.RequestTS).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Other != "{}" {
		t.Fatal("batch touched a different timestamp identity")
	}
	if err := db.Model(&Log{}).Where("id = ? AND request_ts = ?", row.Id, row.RequestTS).Updates(map[string]interface{}{"type": LogTypeUnknown, "other": "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	found, err = UpdateRequestLogDiagnosticsBatch([]RequestDiagnosticsEvent{{RequestID: requestID, Fields: map[string]interface{}{"gateway_pre_ms": 7}}})
	if err != nil || !found[requestID] {
		t.Fatalf("expired PG row must be acknowledged: %v %v", found, err)
	}
	if err := UpdateRequestLogDiagnostics(requestID, map[string]interface{}{"gateway_post_ms": 9}); err != nil {
		t.Fatal(err)
	}
	stored = Log{}
	if err := db.Where("id = ? AND request_ts = ?", row.Id, row.RequestTS).Take(&stored).Error; err != nil || stored.Other != "{}" {
		t.Fatalf("expired PG diagnostics resurrected: %q %v", stored.Other, err)
	}
}
