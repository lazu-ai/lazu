package controller

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
)

func servedOf(models string, mapping string) upstreamServed {
	channel := &model.Channel{Models: models}
	if mapping != "" {
		channel.ModelMapping = &mapping
	}
	return newUpstreamServed(channel)
}

func TestDiffUpstreamFirstListFlagsNothingNew(t *testing.T) {
	cfg := (&dto.UpstreamSyncSettings{IntervalHours: 6}).Normalized()
	served := servedOf("a,b", "")
	diff := diffUpstream(cfg, nil, served, []string{"a", "b", "c"}, 100, false)
	if len(diff.state.New) != 0 || len(diff.entry.New) != 0 {
		t.Fatalf("the first list has nothing to compare with, got new %v", diff.state.New)
	}
	if !reflect.DeepEqual(diff.state.Upstream, []string{"a", "b", "c"}) || diff.state.ListedAt != 100 {
		t.Fatalf("the first list must be kept for the next check, got %+v", diff.state)
	}
	if len(diff.state.Log) != 1 {
		t.Fatalf("every check leaves a log line, got %d", len(diff.state.Log))
	}
}

func TestDiffUpstreamReviewHoldsNewModels(t *testing.T) {
	cfg := (&dto.UpstreamSyncSettings{IntervalHours: 6, Ignore: []string{"re:^antigravity/", "*-preview"}}).Normalized()
	prev := &dto.UpstreamSyncState{ListedAt: 1, Upstream: []string{"a", "b"}}
	served := servedOf("a,b", "")
	listed := []string{"a", "b", "c", "antigravity/a", "g-preview"}
	diff := diffUpstream(cfg, prev, served, listed, 100, false)
	if !reflect.DeepEqual(diff.state.New, []string{"c"}) {
		t.Fatalf("only c is new and not ignored, got %v", diff.state.New)
	}
	if len(diff.add) != 0 {
		t.Fatalf("review mode serves nothing by itself, got %v", diff.add)
	}
}

func TestDiffUpstreamAutoServesNewModels(t *testing.T) {
	cfg := (&dto.UpstreamSyncSettings{IntervalHours: 6, OnNew: dto.UpstreamOnNewAuto}).Normalized()
	prev := &dto.UpstreamSyncState{ListedAt: 1, Upstream: []string{"a", "held"}, New: []string{"held"}}
	diff := diffUpstream(cfg, prev, servedOf("a", ""), []string{"a", "held", "c"}, 100, false)
	if !reflect.DeepEqual(diff.add, []string{"c"}) {
		t.Fatalf("auto mode serves what just appeared, got %v", diff.add)
	}
	if !reflect.DeepEqual(diff.state.New, []string{"held"}) {
		t.Fatalf("a model held under review earlier stays held, got %v", diff.state.New)
	}
}

func TestDiffUpstreamStopsGoneModelsThroughMapping(t *testing.T) {
	cfg := (&dto.UpstreamSyncSettings{IntervalHours: 6}).Normalized()
	prev := &dto.UpstreamSyncState{ListedAt: 1, Upstream: []string{"a", "vendor/b", "c", "d"}}
	served := servedOf("a,b,c,d", `{"b":"vendor/b"}`)
	diff := diffUpstream(cfg, prev, served, []string{"a", "vendor/b", "d"}, 100, false)
	if !reflect.DeepEqual(diff.stop, []string{"c"}) {
		t.Fatalf("b is listed under its upstream ID; only c is gone, got %v", diff.stop)
	}
	if !reflect.DeepEqual(diff.state.Stopped, []string{"c"}) || len(diff.state.Gone) != 0 {
		t.Fatalf("a stopped model is kept on record, got %+v", diff.state)
	}
}

func TestDiffUpstreamKeepModeOnlyFlags(t *testing.T) {
	cfg := (&dto.UpstreamSyncSettings{IntervalHours: 6, OnGone: dto.UpstreamOnGoneKeep}).Normalized()
	prev := &dto.UpstreamSyncState{ListedAt: 1, Upstream: []string{"a", "b"}}
	diff := diffUpstream(cfg, prev, servedOf("a,b", ""), []string{"a"}, 100, false)
	if len(diff.stop) != 0 || !reflect.DeepEqual(diff.state.Gone, []string{"b"}) {
		t.Fatalf("keep mode flags b and serves it on, got stop %v gone %v", diff.stop, diff.state.Gone)
	}
}

func TestDiffUpstreamHoldsBackWhenMostModelsVanish(t *testing.T) {
	cfg := (&dto.UpstreamSyncSettings{IntervalHours: 6}).Normalized()
	prev := &dto.UpstreamSyncState{ListedAt: 1, Upstream: []string{"a", "b", "c", "d"}}
	diff := diffUpstream(cfg, prev, servedOf("a,b,c,d", ""), []string{"a", "x"}, 100, false)
	if len(diff.stop) != 0 {
		t.Fatalf("three of four gone at once is more likely a broken list, got stop %v", diff.stop)
	}
	if len(diff.state.Gone) != 3 || !strings.Contains(diff.entry.Note, "没有自动停止") {
		t.Fatalf("the models are flagged and the log says why, got %+v", diff.entry)
	}
}

func TestDiffUpstreamStoppedModelComesBackAsNew(t *testing.T) {
	cfg := (&dto.UpstreamSyncSettings{IntervalHours: 6}).Normalized()
	prev := &dto.UpstreamSyncState{ListedAt: 1, Upstream: []string{"a"}, Stopped: []string{"b"}}
	diff := diffUpstream(cfg, prev, servedOf("a", ""), []string{"a", "b"}, 100, false)
	if len(diff.state.Stopped) != 0 || !reflect.DeepEqual(diff.state.New, []string{"b"}) {
		t.Fatalf("a stopped model listed again is offered as new, got %+v", diff.state)
	}
}

func TestReconcileUpstreamStateDropsHandledModels(t *testing.T) {
	channel := &model.Channel{Models: "a,c"}
	state := &dto.UpstreamSyncState{New: []string{"c", "d"}, Gone: []string{"a", "b"}, Stopped: []string{"c", "e"}}
	got := reconcileUpstreamState(state, (&dto.UpstreamSyncSettings{}).Normalized(), channel)
	if !reflect.DeepEqual(got.New, []string{"d"}) || !reflect.DeepEqual(got.Gone, []string{"a"}) || !reflect.DeepEqual(got.Stopped, []string{"e"}) {
		t.Fatalf("handled models drop off, got %+v", got)
	}
}

func TestUpstreamLogKeepsTheNewestThirty(t *testing.T) {
	var log []dto.UpstreamSyncLogEntry
	for i := 0; i < 40; i++ {
		log = appendUpstreamLog(log, dto.UpstreamSyncLogEntry{At: int64(i)})
	}
	if len(log) != upstreamSyncLogLimit || log[0].At != 39 {
		t.Fatalf("log is newest first and capped, got %d entries starting at %d", len(log), log[0].At)
	}
}

func TestCompileUpstreamIgnore(t *testing.T) {
	ignore, err := dto.CompileUpstreamIgnore([]string{"gpt-*-mini", "re:^old/", "  "})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"gpt-5.4-mini": true, "gpt-5.4": false, "old/x": true, "x/old/": false} {
		if got := ignore.Match(id); got != want {
			t.Fatalf("Match(%q) = %v, want %v", id, got, want)
		}
	}
	if _, err := dto.CompileUpstreamIgnore([]string{"re:("}); err == nil {
		t.Fatal("a broken regular expression is refused")
	}
}
