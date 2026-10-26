package service

import "time"

// Functions only this package's tests call: they reset state between tests
// or wait for background work to finish. Kept out of the production build.

// From low_balance_notify_gate.go.
func stopLowBalanceMemoryCleanup() {
	lowBalanceCleanupMu.Lock()
	defer lowBalanceCleanupMu.Unlock()
	if lowBalanceCleanupCancel != nil {
		lowBalanceCleanupCancel()
		lowBalanceCleanupCancel = nil
	}
}

// From support_report.go.
// resetSupportReportCounts clears the in-memory counter (tests).
func resetSupportReportCounts() {
	m := &supportReportCountsMem
	m.Lock()
	m.window, m.count = map[int]time.Time{}, map[int]int{}
	m.Unlock()
}

// From token_usage_alert.go.
// waitTokenUsageAlerts joins admitted alerts, including SendMail's delivery
// recorder. Call only after producers have stopped, such as fixture teardown;
// this is not a barrier against concurrent new admissions.
func waitTokenUsageAlerts() {
	tokenUsageAlertJobs.Wait()
}
