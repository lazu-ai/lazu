package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestTokenModelLimitAllowsBothShapes(t *testing.T) {
	limitsOf := func(raw string) map[string]bool {
		token := &Token{ModelLimits: raw}
		return token.GetModelLimitsMap()
	}
	cases := []struct {
		name   string
		limits string
		model  string
		want   bool
	}{
		{"empty denies", "", "gpt-5.5", false},
		{"allowlist admits listed", "gpt-5.5,claude-opus-5-5", "gpt-5.5", true},
		{"allowlist denies others", "gpt-5.5,claude-opus-5-5", "deepseek-v4-pro", false},
		{"exclude admits unlisted", "*,-gpt-5.5", "claude-opus-5-5", true},
		{"exclude admits models added later", "*,-gpt-5.5", "a-model-from-next-month", true},
		{"exclude denies listed", "*,-gpt-5.5", "gpt-5.5", false},
		{"bare star admits everything", "*", "gpt-5.5", true},
		{"allowlist never reads a dash as exclusion", "gpt-5.5", "-gpt-5.5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TokenModelLimitAllows(limitsOf(tc.limits), tc.model); got != tc.want {
				t.Fatalf("TokenModelLimitAllows(%q, %q) = %v, want %v", tc.limits, tc.model, got, tc.want)
			}
		})
	}
}

func TestTokenModelScopeAllowsExcludeIgnoresVendors(t *testing.T) {
	token := &Token{ModelLimits: "*,-gpt-5.5"}
	limits := token.GetModelLimitsMap()
	if !TokenModelScopeAllows(limits, nil, "claude-opus-5-5") {
		t.Fatal("an everything-except Key with no vendors must still admit unlisted models")
	}
	if TokenModelScopeAllows(limits, map[int]struct{}{1: {}}, "gpt-5.5") {
		t.Fatal("an excluded model stays excluded whatever vendors are in scope")
	}
}

func TestNormalizeTokenModelLimits(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{" gpt-5.5 ", "", "gpt-5.5", "claude-opus-5-5"}, []string{"gpt-5.5", "claude-opus-5-5"}},
		{[]string{"-gpt-5.5", "*", "-claude-opus-5-5", "-gpt-5.5", "stray"}, []string{"*", "-gpt-5.5", "-claude-opus-5-5"}},
		{[]string{"-gpt-5.5", "claude-opus-5-5"}, []string{"claude-opus-5-5"}},
		{[]string{"*", "-"}, []string{"*"}},
		{nil, []string{}},
	}
	for _, tc := range cases {
		if got := NormalizeTokenModelLimits(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("NormalizeTokenModelLimits(%q) = %q, want %q", strings.Join(tc.in, ","), got, tc.want)
		}
	}
}

func TestKeepStoredUpstreamSyncState(t *testing.T) {
	stored := `{"cost_multiplier":0.5,"upstream_sync_state":{"checked_at":200,"new":["a"]}}`
	channel := &Channel{OtherSettings: `{"cost_multiplier":0.7,"upstream_sync":{"interval_hours":6},"upstream_sync_state":{"checked_at":100}}`}
	if err := keepStoredUpstreamSyncState(channel, stored); err != nil {
		t.Fatal(err)
	}
	got := channel.GetOtherSettings()
	if got.CostMultiplier != 0.7 || got.UpstreamSync == nil || got.UpstreamSync.IntervalHours != 6 {
		t.Fatalf("the save's own settings must survive, got %+v", got)
	}
	if got.UpstreamSyncState == nil || got.UpstreamSyncState.CheckedAt != 200 || len(got.UpstreamSyncState.New) != 1 {
		t.Fatalf("the stored state must win over the form's copy, got %+v", got.UpstreamSyncState)
	}

	fresh := &Channel{OtherSettings: `{"upstream_sync_state":{"checked_at":100}}`}
	if err := keepStoredUpstreamSyncState(fresh, `{}`); err != nil {
		t.Fatal(err)
	}
	if fresh.GetOtherSettings().UpstreamSyncState != nil {
		t.Fatal("a form cannot plant state the gateway never wrote")
	}

	empty := &Channel{}
	if err := keepStoredUpstreamSyncState(empty, stored); err != nil || empty.OtherSettings != "" {
		t.Fatalf("a save without settings leaves the column alone, got %q, %v", empty.OtherSettings, err)
	}
}
