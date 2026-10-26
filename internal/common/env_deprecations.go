package common

// deprecatedEnvNotices names environment settings that are on their way out
// and are set away from their default, so the operator hears about it at
// startup. Each notice keeps the removal date that docs/DEPLOYMENT.md
// ("Deprecated settings") promises. Option-table settings are covered by
// model/option_deprecations.go.
func deprecatedEnvNotices() []string {
	var notices []string
	if SettlementOutboxEnabled {
		notices = append(notices, "SETTLEMENT_OUTBOX_ENABLED=true is deprecated and will be removed after 2027-01-24, "+
			"with the settlement outbox reconciler; failed settlements stay covered by the failure reconciler "+
			"(SETTLEMENT_FAILURE_RECONCILER_ENABLED). Keep it on until the outbox holds no unresolved rows, then unset it "+
			"(docs/DEPLOYMENT.md, Deprecated settings).")
	}
	return notices
}
