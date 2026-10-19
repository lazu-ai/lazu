package model

import (
	"strings"
	"testing"
)

func TestAdminAuditDiffJSONMasksSensitiveFields(t *testing.T) {
	diff := AdminAuditDiffJSON(
		map[string]any{
			"name":      "before",
			"key":       "sk-before",
			"password":  "old-password",
			"unchanged": "same",
		},
		map[string]any{
			"name":      "after",
			"key":       "sk-after",
			"password":  "new-password",
			"unchanged": "same",
		},
	)

	if strings.Contains(diff, "sk-before") || strings.Contains(diff, "sk-after") || strings.Contains(diff, "password") && strings.Contains(diff, "new-password") {
		t.Fatalf("diff leaked sensitive values: %s", diff)
	}
	if !strings.Contains(diff, "***changed***") {
		t.Fatalf("diff did not mask changed sensitive values: %s", diff)
	}
	if strings.Contains(diff, "unchanged") {
		t.Fatalf("diff should omit unchanged fields: %s", diff)
	}
	if !strings.Contains(diff, "before") || !strings.Contains(diff, "after") {
		t.Fatalf("diff should include changed non-sensitive values: %s", diff)
	}
}
