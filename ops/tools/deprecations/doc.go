// Package deprecations holds the test that fails once a deprecated path
// outlives the release it promised to be deleted by.
//
// A predecessor kept alive for compatibility carries one comment line:
//
//	// Deprecated: delete after v0.7.260 (replaced by X)
//
// TestNoExpiredDeprecations scans Go and TypeScript sources for that line
// and fails when the version named is at or below LAZU_CHECK_VERSION. A
// plain test run sets no version (the binary's own version is v0.0.0), so
// the test skips; CI passes the newest release tag and requires it.
package deprecations
