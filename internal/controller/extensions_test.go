package controller

import "testing"

// exactCreditTestSetups let an extension's test files switch off anything
// that changes a top-up's credit in tests that assert the exact amount.
var exactCreditTestSetups []func(t *testing.T)
