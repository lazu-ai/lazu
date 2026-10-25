package model

import "strings"

// deprecatedOptionNotices names settings that are on their way out and are
// configured away from their default, so the operator hears about it at
// startup rather than when the setting stops working. Each entry keeps the
// removal date that docs/DEPLOYMENT.md ("Deprecated settings") promises.
var deprecatedOptions = []struct {
	key    string
	active func(value string) bool
	notice string
}{
	{
		key:    "LogConsumeEnabled",
		active: func(value string) bool { return strings.TrimSpace(value) == "false" },
		notice: "LogConsumeEnabled=false is deprecated and will be removed after 2027-01-24; " +
			"consume logs will then always be written. The usage pages, usage digests and route health read them. " +
			"Bound their storage with LOG_RETENTION_DAYS instead (docs/DEPLOYMENT.md, Deprecated settings).",
	},
	{
		key:    "WorkerUrl",
		active: func(value string) bool { return strings.TrimSpace(value) != "" },
		notice: "WorkerUrl is deprecated and will be removed after 2027-01-24, with WorkerValidKey and " +
			"WorkerAllowHttpImageRequestEnabled; media downloads and webhooks will then connect directly, " +
			"under the media fetch policy (docs/DEPLOYMENT.md, Deprecated settings).",
	},
}

func deprecatedOptionNotices(options []*Option) []string {
	var notices []string
	for _, option := range options {
		if option == nil {
			continue
		}
		for _, deprecated := range deprecatedOptions {
			if option.Key == deprecated.key && deprecated.active(option.Value) {
				notices = append(notices, deprecated.notice)
			}
		}
	}
	return notices
}
