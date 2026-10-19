package controller

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestResolveDashboardScopeRole(t *testing.T) {
	tests := []struct {
		name     string
		role     int
		scope    string
		expected int
	}{
		{name: "operator customer self view", role: common.RoleRootUser, scope: "self", expected: common.RoleCommonUser},
		{name: "operator platform default", role: common.RoleRootUser, scope: "platform", expected: common.RoleRootUser},
		{name: "member cannot elevate", role: common.RoleCommonUser, scope: "platform", expected: common.RoleCommonUser},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := resolveDashboardScopeRole(test.role, test.scope); actual != test.expected {
				t.Fatalf("expected role %d, got %d", test.expected, actual)
			}
		})
	}
}
