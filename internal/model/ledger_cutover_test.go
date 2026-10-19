package model

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

func TestLedgerCutoverBlocksOnlyActiveLegacyMediaReservations(t *testing.T) {
	db, _ := newLedgerTestDBs(t)
	for _, row := range []MediaJob{
		{ID: 1, PublicID: "legacy-active", Status: MediaJobStatusRunning, RequestSnapshot: JSONValue(`{"_billing_reserved_quota_microusd":80}`)},
		{ID: 2, PublicID: "ledger-active", Status: MediaJobStatusRunning, LedgerInputJSON: `{"request_id":"new"}`, RequestSnapshot: JSONValue(`{"_billing_reserved_quota_microusd":80}`)},
		{ID: 3, PublicID: "legacy-done", Status: MediaJobStatusSucceeded, RequestSnapshot: JSONValue(`{"_billing_reserved_quota_microusd":80}`)},
		{ID: 4, PublicID: "legacy-unreserved", Status: MediaJobStatusPending, RequestSnapshot: JSONValue(`{}`)},
	} {
		if err := db.Session(&gorm.Session{SkipHooks: true}).Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	report, err := ReportLedgerCutoverReadiness(context.Background(), db)
	if err != nil || report.BlockingLegacyMediaReservations != 1 || len(report.LegacyMediaJobIDs) != 1 || report.LegacyMediaJobIDs[0] != 1 {
		t.Fatalf("wrong cutover readiness: %+v %v", report, err)
	}
	if err := validateLedgerCutover(context.Background(), db); err == nil {
		t.Fatal("cutover accepted an untracked legacy reservation")
	}
	if err := db.Model(&MediaJob{}).Where("id = ?", 1).UpdateColumn("status", MediaJobStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateLedgerCutover(context.Background(), db); err != nil {
		t.Fatalf("completed legacy reservation still blocks cutover: %v", err)
	}
}
