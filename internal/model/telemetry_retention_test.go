package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/pkg/billing"
	"gorm.io/gorm"
)

func TestPostgresTelemetryRetentionPreservesIndependentPolicies(t *testing.T) {
	for _, days := range []int{0, 30, 180, 600} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			core, db := postgresTelemetryFixture(t)
			now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
			at := func(age int) time.Time { return now.Add(-time.Duration(age) * 24 * time.Hour) }
			seed := func(id string, createdAt, decisionAt, outcomeAt, admissionAt time.Time) Log {
				t.Helper()
				decision := RoutingDecision{ID: stableRoutingDecisionID(id), RequestID: id, DecisionAt: decisionAt, IsPrimary: true}
				channelID, status := 7, 200
				outcome := RoutingOutcome{RequestID: id, OutcomeAt: outcomeAt, ModelName: "retention-model", FinalGroup: ChannelLaneDirect, ChannelID: &channelID, StatusCode: &status}
				row := Log{PublicID: "log_" + id, RequestID: id, RequestTS: admissionAt.UnixMilli(), CreatedAt: createdAt.Unix(), Type: LogTypeConsume, UserId: 1, Username: "synthetic-user", TokenId: 7, TokenName: "synthetic-key", ProjectID: 7, PayerUserID: 1, Ip: "192.0.2.1", Content: "synthetic-private-content", ModelName: "retention-model", AmountMicrousd: 123, PricingVersion: 9, PromptTokens: 10, CompletionTokens: 20, Other: `{"synthetic_private":true}`, UsageFact: "synthetic-usage", SessionID: "session", AgentID: "agent", ClientCountry: "ZZ", LineItems: "synthetic-items", ThreadID: "thread", ClientVersion: "fixture", RequestHost: "fixture.invalid"}
				foldRelayRoutingIntoLog(&row, relayResultEvent{RoutingDecision: &decision, RoutingOutcome: &outcome})
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
				return row
			}
			detailDays := days
			if detailDays == 0 {
				detailDays = 180
			}
			seed("detail-boundary", at(detailDays), at(detailDays), at(detailDays), at(detailDays))
			seed("detail-expired", at(detailDays+1), at(detailDays+1), at(detailDays+1), at(detailDays+1))
			seed("middle", at(120), at(120), at(120), at(120))
			seed("decision-boundary", at(90), at(90), at(90), at(90))
			seed("outcome-boundary", at(365), at(365), at(365), at(365))
			seed("outcome-expired", at(366), at(366), at(366), at(366))
			old := seed("fully-old", at(700), at(700), at(700), at(700))
			late := seed("long-media", at(1), at(900), at(1), at(900))
			for _, age := range []int{30, 31} {
				if err := db.Create(&ChannelProbeResult{RequestTS: at(age).UnixMilli(), Id: int64(age), ProbedAt: at(age), ChannelId: 7, Success: true}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&ChannelProbeResult{RequestTS: at(900).UnixMilli(), Id: 900, ProbedAt: at(1), ChannelId: 7, Success: true}).Error; err != nil {
				t.Fatal(err)
			}
			for _, age := range []int{90, 91} {
				if err := db.Create(&RoutingAttemptOutcome{RequestID: fmt.Sprintf("attempt-%d", age), RequestTS: at(age).UnixMilli(), OutcomeAt: at(age), ChannelID: 7}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&RoutingAttemptOutcome{RequestID: "late-attempt", RequestTS: at(900).UnixMilli(), OutcomeAt: at(1), ChannelID: 7}).Error; err != nil {
				t.Fatal(err)
			}
			legacyDecision := RoutingDecision{RequestID: "legacy-decision-old", DecisionAt: at(91), IsPrimary: true}
			if err := db.Create(&legacyDecision).Error; err != nil {
				t.Fatal(err)
			}
			channelID, status := 7, 200
			if err := db.Create(&RoutingOutcome{RequestID: "legacy-outcome-middle", OutcomeAt: at(120), ModelName: "retention-model", FinalGroup: ChannelLaneDirect, ChannelID: &channelID, StatusCode: &status}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&RoutingOutcome{RequestID: "legacy-outcome-old", OutcomeAt: at(366)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := migrateTelemetryPartitions(db); err != nil {
				t.Fatal(err)
			}
			// Production's archive was renamed at schema 24 and lacks every
			// Log column added since; retention must still clear what it has.
			if err := db.Exec("ALTER TABLE logs_unpartitioned_v24 DROP COLUMN IF EXISTS gateway_pre_ms, DROP COLUMN IF EXISTS gateway_post_ms").Error; err != nil {
				t.Fatal(err)
			}
			// Retention must not alter permanent money, daily/hourly aggregates
			// or receipts that prevent a late redelivery counting twice.
			if err := core.AutoMigrate(&RedisLedgerOperation{}, &WalletLedger{}, &ProjectSpend{}); err != nil {
				t.Fatal(err)
			}
			protected := []struct {
				db  *gorm.DB
				row interface{}
			}{
				{core, &RedisLedgerOperation{ID: "settle:protected", Amount: 123, Input: "{}", CreatedAt: at(700)}},
				{core, &WalletLedger{UserID: 1, DeltaMicrousd: -123, Kind: WalletLedgerKindSpend, RefID: "protected", CreatedAt: at(700)}},
				{core, &ProjectSpend{EventKey: "protected", ProjectID: 7, AmountMicrousd: billing.MicroUSD(123), OccurredAt: at(700)}},
				{db, &TelemetryEventReceipt{EventKey: "protected", RequestTS: at(700).UnixMilli(), CreatedAt: at(700)}},
				{db, &DashboardUsageDailyDimension{DayStart: at(700).Unix(), DimensionType: "user", DimensionID: 1, ModelName: "retention-model", AmountMicrousd: 123}},
				{db, &DashboardUsageHourlyTotal{BucketHour: at(700).Unix(), AmountMicrousd: 123}},
			}
			for _, item := range protected {
				if err := item.db.Create(item.row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := MaintainTelemetryPartitions(context.Background(), now, days); err != nil {
				t.Fatal(err)
			}
			count := func(row interface{}, query string, id interface{}, want int64) {
				t.Helper()
				var actual int64
				if err := db.Model(row).Where(query, id).Count(&actual).Error; err != nil || actual != want {
					t.Fatalf("retained %T %v count=%d want%d err=%v", row, id, actual, want, err)
				}
			}
			load := func(id string) Log {
				t.Helper()
				var row Log
				if err := db.Where("request_id = ?", id).First(&row).Error; err != nil {
					t.Fatal(err)
				}
				return row
			}
			boundary := load("detail-boundary")
			if boundary.Type != LogTypeConsume || boundary.Content != "synthetic-private-content" {
				t.Fatal("detail exactly at its retention boundary was removed")
			}
			if days == 0 {
				row := load("fully-old")
				if row.Type != LogTypeConsume || row.Ip != "192.0.2.1" || !db.Migrator().HasTable("logs_"+time.UnixMilli(old.RequestTS).UTC().Format("200601")) {
					t.Fatal("disabled log retention stripped data or dropped its partition")
				}
			} else {
				// A 601-day detail may share an entirely expired partition and
				// be dropped; shorter cases still carry a valid outcome.
				if days < RoutingOutcomeRetentionDays {
					row := load("detail-expired")
					if row.Type != LogTypeUnknown || row.Ip != "" || row.UserId != 0 || row.ProjectID != 0 || row.TokenName != "" || row.Content != "" || row.Other != "" || row.UsageFact != "" || row.AmountMicrousd != 0 || row.SessionID != "" || row.ThreadID != "" || row.LineItems != "" || row.PricingVersion != 0 {
						t.Fatalf("expired consume details survived: %+v", row)
					}
				}
				if db.Migrator().HasTable("logs_" + time.UnixMilli(old.RequestTS).UTC().Format("200601")) {
					t.Fatal("fully expired old logs partition was retained")
				}
			}
			middle := load("middle")
			if middle.RoutingDecisionJSON != "" || middle.RoutingOutcomeJSON == "" || middle.RoutingDecision.ID != nil || middle.RoutingOutcome.RequestID == nil {
				t.Fatal("90-day decision/365-day outcome lifetimes were conflated")
			}
			if days == 180 && middle.Type != LogTypeConsume {
				t.Fatal("custom retention above90 was overwritten by routing cleanup")
			}
			count(&RoutingDecision{}, "request_id = ?", "decision-boundary", 1)
			count(&RoutingDecision{}, "request_id = ?", "legacy-decision-old", 0)
			count(&RoutingOutcome{}, "request_id = ?", "outcome-boundary", 1)
			count(&RoutingOutcome{}, "request_id = ?", "outcome-expired", 0)
			count(&RoutingOutcome{}, "request_id = ?", "legacy-outcome-middle", 1)
			count(&RoutingOutcome{}, "request_id = ?", "legacy-outcome-old", 0)
			var archived int64
			if err := db.Table("logs_unpartitioned_v24").Where("request_id = ?", "fully-old").Count(&archived).Error; err != nil || days > 0 && archived != 0 || days == 0 && archived != 1 {
				t.Fatalf("migration raw archive ignored configured lifetime: count=%d err=%v", archived, err)
			}
			count(&ChannelProbeResult{}, "id = ?", 30, 1)
			count(&ChannelProbeResult{}, "id = ?", 31, 0)
			count(&ChannelProbeResult{}, "id = ?", 900, 1)
			count(&RoutingAttemptOutcome{}, "request_id = ?", "attempt-90", 1)
			count(&RoutingAttemptOutcome{}, "request_id = ?", "attempt-91", 0)
			count(&RoutingAttemptOutcome{}, "request_id = ?", "late-attempt", 1)
			if load("long-media").RoutingOutcomeJSON == "" || !db.Migrator().HasTable("logs_"+time.UnixMilli(late.RequestTS).UTC().Format("200601")) {
				t.Fatal("old admission partition removed a recently completed media fact")
			}
			for _, spec := range telemetryPartitionSpecs {
				if !db.Migrator().HasTable(spec.table + "_202611") {
					t.Fatalf("month-end maintenance skipped next month for %s", spec.table)
				}
			}
			for _, item := range protected {
				var total int64
				if err := item.db.Model(item.row).Count(&total).Error; err != nil || total != 1 {
					t.Fatalf("retention altered protected %T count=%d err=%v", item.row, total, err)
				}
			}
			listed, _, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", 0, "", 0, 100, 0, "")
			if err != nil {
				t.Fatal(err)
			}
			exported, err := ListDashboardUsageLogs(0, common.RoleRootUser, DashboardUsageWindow{}, DashboardUsageLogFilters{}, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range exported {
				if row.Type == LogTypeUnknown {
					t.Fatal("usage export exposed an expired routing carrier")
				}
			}
			for _, row := range listed {
				if row.Type == LogTypeUnknown {
					t.Fatal("default log listing exposed an expired routing carrier")
				}
			}
			// A repeat cannot revive legacy projections or remove a retained
			// fact merely because its consume part already became Unknown.
			if err := MaintainTelemetryPartitions(context.Background(), now, days); err != nil {
				t.Fatal(err)
			}
			count(&RoutingOutcome{}, "request_id = ?", "outcome-boundary", 1)
		})
	}
}

func TestPostgresExpiredRelayRedeliveryKeepsPermanentAggregates(t *testing.T) {
	_, db := postgresTelemetryFixture(t)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	old := now.Add(-700 * 24 * time.Hour)
	// Ensure the initial migration creates the old admission partition.
	if err := db.Create(&Log{RequestID: "old-partition-seed", CreatedAt: old.Unix(), RequestTS: old.UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateTelemetryPartitions(db); err != nil {
		t.Fatal(err)
	}
	event := telemetryFixture("expired-redelivery")
	event.ConsumeLog.CreatedAt, event.ConsumeLog.RequestTS = old.Unix(), old.UnixMilli()
	event.RoutingDecision.DecisionAt = old
	event.RoutingDecision.ID = stableRoutingDecisionID(event.RoutingDecision.RequestID)
	event.RoutingOutcome.OutcomeAt, event.RoutingOutcome.DecisionID = old, event.RoutingDecision.ID
	event.RoutingAttempts[0].OutcomeAt, event.RoutingAttempts[0].RequestTS = old, old.UnixMilli()
	if err := persistRelayResultBatch([]relayResultEvent{event}); err != nil {
		t.Fatal(err)
	}
	if err := MaintainTelemetryPartitions(context.Background(), now, 30); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := persistRelayResultBatch([]relayResultEvent{event}); err != nil {
			t.Fatalf("expired redelivery was not acknowledged through its permanent receipt: %v", err)
		}
	}
	var logs, receipts int64
	if err := db.Model(&Log{}).Count(&logs).Error; err != nil || logs != 0 {
		t.Fatalf("redelivery revived expired detail: logs=%d err=%v", logs, err)
	}
	if err := db.Model(&TelemetryEventReceipt{}).Count(&receipts).Error; err != nil || receipts != 1 {
		t.Fatalf("permanent receipt changed: receipts=%d err=%v", receipts, err)
	}
	var counter ModelRequestCounter
	if err := db.First(&counter).Error; err != nil || counter.TotalRequests != 1 {
		t.Fatalf("redelivery recounted model requests: counter=%+v err=%v", counter, err)
	}
	var daily DashboardUsageDailyDimension
	if err := db.Where("dimension_type = ?", "project").First(&daily).Error; err != nil || daily.RequestCount != 1 || daily.AmountMicrousd != 123 {
		t.Fatalf("redelivery recounted daily amount: daily=%+v err=%v", daily, err)
	}
	var hourly DashboardUsageHourlyTotal
	if err := db.Where("scope_user_id = ?", 0).First(&hourly).Error; err != nil || hourly.RequestCount != 1 || hourly.AmountMicrousd != 123 {
		t.Fatalf("redelivery recounted hourly amount: hourly=%+v err=%v", hourly, err)
	}
}
