package model

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

type LedgerCutoverReport struct {
	BlockingLegacyMediaReservations int64  `json:"blocking_legacy_media_reservations"`
	LegacyMediaJobIDs               []uint `json:"legacy_media_job_ids,omitempty"`
}

// Legacy jobs have already debited SQL without a Redis reservation. They must
// finish or be refunded by the previous release before enabling the ledger.
func ReportLedgerCutoverReadiness(ctx context.Context, db *gorm.DB) (LedgerCutoverReport, error) {
	report := LedgerCutoverReport{}
	if db == nil || !db.Migrator().HasTable(&MediaJob{}) {
		return report, nil
	}
	query := db.WithContext(ctx).Model(&MediaJob{}).
		Select("id", "request_snapshot").Where("status IN ?", []string{MediaJobStatusPending, MediaJobStatusRunning})
	if db.Migrator().HasColumn(&MediaJob{}, "ledger_input_json") {
		query = query.Where("COALESCE(ledger_input_json,'') = ''")
	}
	rows, err := query.Order("id").Rows()
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var job MediaJob
		if err := db.ScanRows(rows, &job); err != nil {
			return report, err
		}
		if MediaJobBillingReservationAmount(job) > 0 {
			report.BlockingLegacyMediaReservations++
			if len(report.LegacyMediaJobIDs) < 100 {
				report.LegacyMediaJobIDs = append(report.LegacyMediaJobIDs, job.ID)
			}
		}
	}
	return report, rows.Err()
}

func validateLedgerCutover(ctx context.Context, db *gorm.DB) error {
	report, err := ReportLedgerCutoverReadiness(ctx, db)
	if err != nil {
		return err
	}
	if report.BlockingLegacyMediaReservations > 0 {
		return fmt.Errorf("ledger cutover blocked by %d legacy media reservations: finish or refund them with the previous release", report.BlockingLegacyMediaReservations)
	}
	return nil
}
