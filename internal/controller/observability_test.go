package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func setupObservabilityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	previousDB := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if err := db.AutoMigrate(&model.RoutingDecision{}, &model.RoutingOutcome{}, &model.RoutingAttemptOutcome{}); err != nil {
		t.Fatalf("migrate routing tables: %v", err)
	}
	if err := db.AutoMigrate(model.TelemetrySchemaModels()...); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestParseAnnouncementEffectiveDateAcceptsAdminRoundTrip(t *testing.T) {
	calendar := "2026-09-30"
	parsed, set, err := parseAnnouncementEffectiveDate(&calendar)
	if err != nil || !set || parsed == nil || parsed.Format("2006-01-02") != calendar {
		t.Fatalf("parse calendar date: parsed=%v set=%v err=%v", parsed, set, err)
	}

	rfc3339 := "2026-09-30T00:00:00Z"
	parsed, set, err = parseAnnouncementEffectiveDate(&rfc3339)
	if err != nil || !set || parsed == nil || parsed.Format(time.RFC3339) != rfc3339 {
		t.Fatalf("parse RFC3339 date: parsed=%v set=%v err=%v", parsed, set, err)
	}

	invalid := "09/30/2026"
	if _, _, err := parseAnnouncementEffectiveDate(&invalid); err == nil {
		t.Fatal("expected invalid effective date to fail")
	}
}

func TestQueryAdminHealthRowsCountsInstrumentedErrorsAndP95(t *testing.T) {
	db := setupObservabilityTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&model.RoutingDecision{
		ID:             1,
		RequestID:      "decision-1",
		DecisionAt:     now,
		IsPrimary:      true,
		RequestedModel: "deepseek-v4-flash",
		StrategyName:   "test",
	}).Error; err != nil {
		t.Fatalf("create routing decision: %v", err)
	}
	statusOK := 200
	for index := 1; index <= 20; index++ {
		durationMs := index * 100
		outcome := model.RoutingOutcome{
			RequestID:       fmt.Sprintf("request-%02d", index),
			OutcomeAt:       now.Add(time.Duration(index) * time.Second),
			DecisionID:      1,
			StatusCode:      &statusOK,
			TotalDurationMs: &durationMs,
		}
		if index == 1 {
			outcome.ErrorCode = "upstream_timeout"
		}
		if err := db.Create(&outcome).Error; err != nil {
			t.Fatalf("create routing outcome %d: %v", index, err)
		}
	}
	// Caller faults are outside the channel-reliability population. Give them
	// large latencies and explicit errors so the test catches both denominator
	// pollution and percentile pollution if the evidence filter regresses.
	for index, status := range []int{400, 403} {
		durationMs := 60_000 + index
		outcome := model.RoutingOutcome{
			RequestID:       fmt.Sprintf("caller-fault-%d", status),
			OutcomeAt:       now.Add(time.Duration(30+index) * time.Second),
			DecisionID:      1,
			StatusCode:      &status,
			ErrorCode:       "invalid_request",
			ErrorType:       "caller_error",
			TotalDurationMs: &durationMs,
		}
		if err := db.Create(&outcome).Error; err != nil {
			t.Fatalf("create caller fault %d: %v", status, err)
		}
	}

	rows, err := queryAdminHealthRows(now.Add(-time.Hour), "model")
	if err != nil {
		t.Fatalf("query admin health rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one model row, got %#v", rows)
	}
	if rows[0].RequestCount != 20 {
		t.Fatalf("expected 20 channel-evidence requests without caller faults, got %d", rows[0].RequestCount)
	}
	if rows[0].ErrorCount != 1 {
		t.Fatalf("instrumented error_code should count as an error, got %d", rows[0].ErrorCount)
	}
	if rows[0].P95LatencyMs != 1900 {
		t.Fatalf("expected nearest-rank p95 1900ms, got %.0f", rows[0].P95LatencyMs)
	}
}

func TestQueryAdminHealthRowsAggregatesByChannelAndModelChannel(t *testing.T) {
	db := setupObservabilityTestDB(t)
	now := time.Now().UTC()
	if err := db.Exec("CREATE TABLE channels (id integer primary key, name text)").Error; err != nil {
		t.Fatalf("create channels table: %v", err)
	}
	if err := db.Exec("INSERT INTO channels (id, name) VALUES (?, ?), (?, ?)", 11, "direct", 12, "backup").Error; err != nil {
		t.Fatalf("seed channels: %v", err)
	}
	decisions := []model.RoutingDecision{
		{
			ID:             1,
			RequestID:      "decision-a",
			DecisionAt:     now,
			IsPrimary:      true,
			RequestedModel: "deepseek-v4-flash",
			StrategyName:   "test",
		},
		{
			ID:             2,
			RequestID:      "decision-b",
			DecisionAt:     now,
			IsPrimary:      true,
			RequestedModel: "gpt-4.1-mini",
			StrategyName:   "test",
		},
	}
	if err := db.Create(&decisions).Error; err != nil {
		t.Fatalf("create routing decisions: %v", err)
	}
	statusOK := 200
	statusFail := 503
	channelDirect := 11
	channelBackup := 12
	outcomes := []model.RoutingOutcome{
		{
			RequestID:       "request-a-1",
			OutcomeAt:       now.Add(time.Second),
			DecisionID:      1,
			ChannelID:       &channelDirect,
			StatusCode:      &statusOK,
			TotalDurationMs: intPtr(100),
		},
		{
			RequestID:       "request-a-2",
			OutcomeAt:       now.Add(2 * time.Second),
			DecisionID:      1,
			ChannelID:       &channelDirect,
			StatusCode:      &statusFail,
			TotalDurationMs: intPtr(500),
		},
		{
			RequestID:       "request-b-1",
			OutcomeAt:       now.Add(3 * time.Second),
			DecisionID:      2,
			ChannelID:       &channelDirect,
			StatusCode:      &statusOK,
			TotalDurationMs: intPtr(300),
		},
		{
			RequestID:       "request-a-3",
			OutcomeAt:       now.Add(4 * time.Second),
			DecisionID:      1,
			ChannelID:       &channelBackup,
			StatusCode:      &statusOK,
			TotalDurationMs: intPtr(700),
		},
	}
	if err := db.Create(&outcomes).Error; err != nil {
		t.Fatalf("create routing outcomes: %v", err)
	}
	if err := db.Create(&model.RoutingAttemptOutcome{
		RequestID: "request-a-3", AttemptIndex: 0, OutcomeAt: now.Add(3500 * time.Millisecond),
		DecisionID: 1, ChannelID: channelDirect, FinalGroup: model.ChannelLaneDirect,
		StatusCode: statusFail, ErrorCode: "upstream_error", TotalDurationMs: 900,
	}).Error; err != nil {
		t.Fatalf("create retried routing attempt: %v", err)
	}

	channelRows, err := queryAdminHealthRows(now.Add(-time.Hour), "channel")
	if err != nil {
		t.Fatalf("query channel health rows: %v", err)
	}
	if len(channelRows) != 2 {
		t.Fatalf("expected two channel rows, got %#v", channelRows)
	}
	if channelRows[0].ChannelID == nil || *channelRows[0].ChannelID != channelDirect || channelRows[0].ChannelName != "direct" {
		t.Fatalf("expected direct channel first, got %#v", channelRows[0])
	}
	if channelRows[0].RequestCount != 4 || channelRows[0].ErrorCount != 2 || channelRows[0].P95LatencyMs != 900 {
		t.Fatalf("unexpected direct channel aggregate: %#v", channelRows[0])
	}

	modelChannelRows, err := queryAdminHealthRows(now.Add(-time.Hour), "model_channel")
	if err != nil {
		t.Fatalf("query model-channel health rows: %v", err)
	}
	var deepseekDirect *adminHealthRow
	for index := range modelChannelRows {
		row := &modelChannelRows[index]
		if row.ModelName == "deepseek-v4-flash" && row.ChannelID != nil && *row.ChannelID == channelDirect {
			deepseekDirect = row
			break
		}
	}
	if deepseekDirect == nil {
		t.Fatalf("expected deepseek/direct aggregate, got %#v", modelChannelRows)
	}
	if deepseekDirect.RequestCount != 3 || deepseekDirect.ErrorCount != 2 || deepseekDirect.P95LatencyMs != 900 {
		t.Fatalf("unexpected deepseek/direct aggregate: %#v", *deepseekDirect)
	}
}

func intPtr(value int) *int {
	return &value
}
