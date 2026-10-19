package common

import "testing"

func TestResolveProcessRole(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"", ProcessRoleAllInOne},
		{"all-in-one", ProcessRoleAllInOne},
		{"api", ProcessRoleAPI},
		{" API ", ProcessRoleAPI},
		{"worker", ProcessRoleWorker},
		{"unknown", ProcessRoleAllInOne},
	}
	for _, test := range tests {
		if got := ResolveProcessRole(test.raw); got != test.want {
			t.Fatalf("ResolveProcessRole(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}
