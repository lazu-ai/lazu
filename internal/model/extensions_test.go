package model

import "testing"

// testDBSetupHooks let an extension's test files adjust the shared payment
// fixtures (for example switch off a bonus the core tests do not expect).
var testDBSetupHooks []func(t *testing.T)

func withOperatedAccountRemark(t *testing.T, prefix string) {
	t.Helper()
	previous := extensions.operatedAccountRemarks
	RegisterOperatedAccountRemarkPrefix(prefix)
	t.Cleanup(func() { extensions.operatedAccountRemarks = previous })
}

// testExtensionMediaSource stands for an extension's own media job source.
const testExtensionMediaSource = "test-extension"

func init() {
	RegisterMediaJobSource(testExtensionMediaSource)
}
