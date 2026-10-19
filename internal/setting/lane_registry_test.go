package setting

import "testing"

func resetLanes(t *testing.T) {
	t.Helper()
	if err := UpdateChannelLanesByJSONString(""); err != nil {
		t.Fatalf("reset lanes: %v", err)
	}
	t.Cleanup(func() { _ = UpdateChannelLanesByJSONString("") })
}

func TestDefaultLanesAreEnabledAndBuiltin(t *testing.T) {
	resetLanes(t)
	for _, slug := range []string{LaneSlugDirect, LaneSlugAgent} {
		lane, ok := LaneBySlug(slug)
		if !ok {
			t.Fatalf("%s missing from the registry", slug)
		}
		if !lane.Enabled || !lane.Builtin {
			t.Fatalf("%s should be enabled and builtin, got %+v", slug, lane)
		}
		if !IsLaneEnabled(slug) || !IsLaneKnown(slug) {
			t.Fatalf("%s should be routable and known", slug)
		}
	}
	if IsLaneKnown("turbo") {
		t.Fatal("an unregistered slug must not be known")
	}
}

func TestNormalizeLaneSlugMapsHistoricalAliases(t *testing.T) {
	for _, alias := range []string{"", "default", "auto", " DIRECT "} {
		if got := NormalizeLaneSlug(alias); got != LaneSlugDirect {
			t.Fatalf("NormalizeLaneSlug(%q) = %q, want %q", alias, got, LaneSlugDirect)
		}
	}
	if got := NormalizeLaneSlug(" Turbo "); got != "turbo" {
		t.Fatalf("unknown slug should pass through lowercased, got %q", got)
	}
}

// A new lane must not be routable the moment it is created: nothing supplies it
// yet, so a discount typed against it would apply to no traffic at all.
func TestNewLaneStartsStopped(t *testing.T) {
	resetLanes(t)
	lane, err := UpsertChannelLane(ChannelLane{Slug: "turbo", Name: "极速", Enabled: true})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if lane.Enabled {
		t.Fatal("a freshly created lane must start stopped")
	}
	if !IsLaneKnown("turbo") {
		t.Fatal("a stopped lane is still known")
	}
	if IsLaneEnabled("turbo") {
		t.Fatal("a stopped lane is not routable")
	}
}

func TestLaneDeletionOnlyBeforeItEverRan(t *testing.T) {
	resetLanes(t)
	if _, err := UpsertChannelLane(ChannelLane{Slug: "turbo", Name: "极速"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := DeleteChannelLane("turbo"); err != nil {
		t.Fatalf("a lane that never ran should be deletable: %v", err)
	}

	if _, err := UpsertChannelLane(ChannelLane{Slug: "turbo", Name: "极速"}); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if _, err := SetChannelLaneEnabled("turbo", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := SetChannelLaneEnabled("turbo", false); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := DeleteChannelLane("turbo"); err == nil {
		t.Fatal("a lane that has carried traffic must not be deletable")
	}
	if !IsLaneKnown("turbo") {
		t.Fatal("stopping a lane keeps it known so its prices still resolve")
	}
}

func TestBuiltinLanesCannotBeStoppedOrDeleted(t *testing.T) {
	resetLanes(t)
	if _, err := SetChannelLaneEnabled(LaneSlugDirect, false); err == nil {
		t.Fatal("the direct lane is the fallback and cannot be stopped")
	}
	if err := DeleteChannelLane(LaneSlugAgent); err == nil {
		t.Fatal("builtin lanes cannot be deleted")
	}
}

func TestUpdateChannelLanesReseedsMissingBuiltins(t *testing.T) {
	resetLanes(t)
	if err := UpdateChannelLanesByJSONString(`[{"slug":"turbo","name":"极速","enabled":true}]`); err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, slug := range []string{LaneSlugDirect, LaneSlugAgent} {
		if !IsLaneEnabled(slug) {
			t.Fatalf("%s must be re-seeded when a payload omits it", slug)
		}
	}
	if !IsLaneEnabled("turbo") {
		t.Fatal("turbo should have survived the round trip")
	}
}

func TestUpdateChannelLanesRejectsBadSlugs(t *testing.T) {
	resetLanes(t)
	if err := UpdateChannelLanesByJSONString(`[{"slug":"Turbo Lane","name":"极速"}]`); err == nil {
		t.Fatal("a slug with spaces and capitals must be refused")
	}
	if err := UpdateChannelLanesByJSONString(`[{"slug":"turbo","name":"a"},{"slug":"turbo","name":"b"}]`); err == nil {
		t.Fatal("duplicate slugs must be refused")
	}
}

func TestEnabledLaneSlugsExcludesStopped(t *testing.T) {
	resetLanes(t)
	if _, err := UpsertChannelLane(ChannelLane{Slug: "turbo", Name: "极速"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for _, slug := range EnabledLaneSlugs() {
		if slug == "turbo" {
			t.Fatal("a stopped lane must not appear in the routing set")
		}
	}
	found := false
	for _, slug := range KnownLaneSlugs() {
		if slug == "turbo" {
			found = true
		}
	}
	if !found {
		t.Fatal("a stopped lane must still appear in the pricing set")
	}
}

// A stored registry that still carries an old default name or note for the
// agent lane takes the current one; a name an operator chose stays.
func TestAgentLaneTakesCurrentNameFromOldDefaults(t *testing.T) {
	resetLanes(t)
	builtin, _ := builtinLane(LaneSlugAgent)
	for _, old := range []string{"便宜", "优惠"} {
		stored := `[{"slug":"direct","name":"稳定","enabled":true},{"slug":"cheap","name":"` + old + `","note":"价格更低的一批来源","enabled":true}]`
		if err := UpdateChannelLanesByJSONString(stored); err != nil {
			t.Fatal(err)
		}
		lane, _ := LaneBySlug(LaneSlugAgent)
		if lane.Name != builtin.Name || lane.Note != builtin.Note {
			t.Fatalf("old name %q: lane = %+v, want %q / %q", old, lane, builtin.Name, builtin.Note)
		}
	}
	if err := UpdateChannelLanesByJSONString(`[{"slug":"cheap","name":"夜间池","note":"运营自定义","enabled":true}]`); err != nil {
		t.Fatal(err)
	}
	if lane, _ := LaneBySlug(LaneSlugAgent); lane.Name != "夜间池" || lane.Note != "运营自定义" {
		t.Fatalf("operator name lost: %+v", lane)
	}
}
