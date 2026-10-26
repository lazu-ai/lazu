// Package deprecations holds the test that fails once a deprecated path
// outlives the release or date it promised to be deleted by.
//
// A predecessor kept alive for compatibility carries one comment line, with
// either a release or a date:
//
//	// Deprecated: delete after v0.7.260 (replaced by X)
//	// Deprecated: delete after 2027-01-24 (replaced by X)
//
// Use a date when the promise is a notice period (a setting operators may
// have configured gets at least 90 days); use a release when callers move in
// lockstep with the code.
//
// TestNoExpiredDeprecations scans Go and TypeScript sources for that line. A
// date fails once today (UTC) is past it. A release fails when it is at or
// below LAZU_CHECK_VERSION: a plain test run sets no version (the binary's
// own version is v0.0.0), so release lines are skipped there; CI passes the
// newest release tag and requires it.
package deprecations
