package model

import (
	"reflect"
	"testing"
)

func TestParseEmailDomainWhitelistTreatsEmptyAsNoDomains(t *testing.T) {
	// An empty stored list must be an empty slice: the guard that refuses to
	// enable the restriction checks len == 0, and [""] slipped past it.
	if got := ParseEmailDomainWhitelist(""); len(got) != 0 {
		t.Fatalf("expected no domains for an empty list, got %q", got)
	}
	if got := ParseEmailDomainWhitelist(" , ,"); len(got) != 0 {
		t.Fatalf("expected blank entries to be dropped, got %q", got)
	}
}

func TestParseEmailDomainWhitelistTrimsEntries(t *testing.T) {
	got := ParseEmailDomainWhitelist("lazu.ai, cerul.app ,")
	want := []string{"lazu.ai", "cerul.app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
