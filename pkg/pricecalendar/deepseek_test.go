package pricecalendar

import (
	"testing"
	"time"
)

func TestDeepSeekCalendar(t *testing.T) {
	for _, tt := range []struct {
		at   string
		want float64
	}{
		{"2026-10-20T00:59:59Z", 1}, {"2026-10-20T01:00:00Z", 2},
		{"2026-10-20T03:59:59Z", 2}, {"2026-10-20T04:00:00Z", 1},
		{"2026-10-20T05:59:59Z", 1}, {"2026-10-20T06:00:00Z", 2},
		{"2026-10-20T09:59:59Z", 2}, {"2026-10-20T10:00:00Z", 1},
		{"2026-10-17T02:00:00Z", 1}, {"2026-10-18T02:00:00Z", 1},
		{"2026-10-10T02:00:00Z", 1}, // make-up Saturday is still off-peak
		{"2026-10-01T02:00:00Z", 1}, {"2026-10-07T02:00:00Z", 1},
		{"2026-10-08T02:00:00Z", 2}, {"2026-02-23T02:00:00Z", 1},
		{"2026-05-05T10:00:00+08:00", 1}, {"2026-10-20T10:00:00+08:00", 2},
		{"2027-02-01T02:00:00Z", 1}, // no verified holiday data: no invented surcharge
	} {
		t.Run(tt.at, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tt.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := DeepSeekFactor(at); got != tt.want {
				t.Fatalf("factor=%v want %v", got, tt.want)
			}
		})
	}
}
func TestCalendarExpiryAlert(t *testing.T) {
	if err := CheckCoverage(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := CheckCoverage(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("missing next-year coverage must alert before expiry")
	}
}
