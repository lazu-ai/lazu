package controller

import (
	"github.com/lazu-ai/lazu/internal/model"
	"testing"
)

func TestMissingModelReleaseDatesUsesEffectiveCatalogDates(t *testing.T) {
	rows := []model.Pricing{
		{ModelName: "known", ReleaseDate: "2026-09-03"},
		{ModelName: "legacy", Maker: "openai"},
		{ModelName: "missing-record", Maker: "google"},
		{ModelName: "invalid", ReleaseDate: "2026-02-30"},
	}
	got := missingModelReleaseDates(rows, map[string]int{"legacy": 42})
	if len(got) != 3 || got[0].ModelName != "legacy" || got[0].ModelID != 42 || got[0].Maker != "openai" || got[1].ModelID != 0 {
		t.Fatalf("missing dates: %+v", got)
	}
	if got := missingModelReleaseDates(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("empty catalog must serialize as []: %+v", got)
	}
}
