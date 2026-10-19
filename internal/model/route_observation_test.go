package model

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func setupRouteObservationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupChannelHealthTestDB(t)
	if err := db.AutoMigrate(&RouteObservation{}, &RouteObservationCheck{}); err != nil {
		t.Fatalf("migrate observation tables: %v", err)
	}
	return db
}

func observedRoles(t *testing.T, db *gorm.DB, channelID int) map[string]string {
	t.Helper()
	var rows []Ability
	if err := db.Where("channel_id = ?", channelID).Find(&rows).Error; err != nil {
		t.Fatalf("load abilities: %v", err)
	}
	roles := map[string]string{}
	for _, row := range rows {
		roles[row.Group+"|"+row.Model] = row.Role
	}
	return roles
}

// A source created to be watched brings every route in as inventory, and
// each of those routes starts its observation.
func TestAddAbilitiesParksObservedSourceRoutes(t *testing.T) {
	db := setupRouteObservationTestDB(t)
	channel := Channel{Id: 11, Name: "relay", Type: 1, Status: common.ChannelStatusEnabled,
		Group: ChannelLaneAgent, Models: "gpt-6-sol,claude-fable-5-1",
		OtherSettings: `{"observe_new_routes":true}`}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channel.AddAbilities(nil); err != nil {
		t.Fatalf("AddAbilities: %v", err)
	}
	for key, role := range observedRoles(t, db, 11) {
		if role != AbilityRoleInventory {
			t.Fatalf("%s role = %q, want inventory", key, role)
		}
	}
	watched, err := LoadActiveRouteObservations(nil)
	if err != nil {
		t.Fatalf("LoadActiveRouteObservations: %v", err)
	}
	if len(watched) != 2 {
		t.Fatalf("watched = %+v, want both routes", watched)
	}
}

// A source that is not watched keeps today's behaviour: its routes serve at
// once.
func TestAddAbilitiesWithoutObservationServesAtOnce(t *testing.T) {
	db := setupRouteObservationTestDB(t)
	channel := Channel{Id: 12, Name: "official", Type: 1, Status: common.ChannelStatusEnabled,
		Group: ChannelLaneDirect, Models: "gpt-6-sol"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channel.AddAbilities(nil); err != nil {
		t.Fatalf("AddAbilities: %v", err)
	}
	if role := observedRoles(t, db, 12)[ChannelLaneDirect+"|gpt-6-sol"]; role != AbilityRolePrimary {
		t.Fatalf("role = %q, want primary", role)
	}
	if watched, _ := LoadActiveRouteObservations(nil); len(watched) != 0 {
		t.Fatalf("watched = %+v, want none", watched)
	}
}

// A Codex-only source cannot be reached by any probe, so it is never watched:
// it would sit in inventory forever.
func TestAddAbilitiesNeverObservesCodexOnlySource(t *testing.T) {
	db := setupRouteObservationTestDB(t)
	channel := Channel{Id: 13, Name: "codex", Type: 1, Status: common.ChannelStatusEnabled,
		Group: ChannelLaneAgent, Models: "gpt-6-sol",
		OtherSettings: `{"observe_new_routes":true,"codex_client_only":true}`}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channel.AddAbilities(nil); err != nil {
		t.Fatalf("AddAbilities: %v", err)
	}
	if role := observedRoles(t, db, 13)[ChannelLaneAgent+"|gpt-6-sol"]; role != AbilityRolePrimary {
		t.Fatalf("role = %q, want primary", role)
	}
}

// Editing a watched source watches only the models the edit adds. A model
// that already went live stays live, and a removed model's observation goes
// with it.
func TestUpdateAbilitiesObservesOnlyNewRoutes(t *testing.T) {
	db := setupRouteObservationTestDB(t)
	channel := Channel{Id: 14, Name: "relay", Type: 1, Status: common.ChannelStatusEnabled,
		Group: ChannelLaneAgent, Models: "live-model,old-watched",
		OtherSettings: `{"observe_new_routes":true}`}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channel.AddAbilities(nil); err != nil {
		t.Fatalf("AddAbilities: %v", err)
	}
	// live-model finished its observation and went live.
	if err := db.Model(&Ability{}).Where("channel_id = ? AND model = ?", 14, "live-model").
		Update("role", AbilityRoleBackup).Error; err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := EndRouteObservations(nil, 14, ChannelLaneAgent, []string{"live-model"}); err != nil {
		t.Fatalf("end observation: %v", err)
	}

	channel.Models = "live-model,new-model"
	if err := channel.UpdateAbilities(nil); err != nil {
		t.Fatalf("UpdateAbilities: %v", err)
	}
	roles := observedRoles(t, db, 14)
	if roles[ChannelLaneAgent+"|live-model"] != AbilityRoleBackup {
		t.Fatalf("live-model role = %q, want backup kept", roles[ChannelLaneAgent+"|live-model"])
	}
	if roles[ChannelLaneAgent+"|new-model"] != AbilityRoleInventory {
		t.Fatalf("new-model role = %q, want inventory", roles[ChannelLaneAgent+"|new-model"])
	}
	var rows []RouteObservation
	if err := db.Where("channel_id = ?", 14).Order("model_name").Find(&rows).Error; err != nil {
		t.Fatalf("load observations: %v", err)
	}
	if len(rows) != 1 || rows[0].ModelName != "new-model" {
		t.Fatalf("observations = %+v, want only new-model", rows)
	}
}

// Repairing the ability table must not undo what an operator set: a backup
// stays a backup, a parked spare stays parked, and a route that already went
// live is not watched again.
func TestFixAbilityKeepsRoles(t *testing.T) {
	db := setupRouteObservationTestDB(t)
	channel := Channel{Id: 15, Name: "relay", Type: 1, Status: common.ChannelStatusEnabled,
		Group: ChannelLaneAgent, Models: "went-live,still-watched",
		OtherSettings: `{"observe_new_routes":true}`}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channel.AddAbilities(nil); err != nil {
		t.Fatalf("AddAbilities: %v", err)
	}
	if err := db.Model(&Ability{}).Where("channel_id = ? AND model = ?", 15, "went-live").
		Update("role", AbilityRoleBackup).Error; err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := EndRouteObservations(nil, 15, ChannelLaneAgent, []string{"went-live"}); err != nil {
		t.Fatalf("end observation: %v", err)
	}
	var before RouteObservation
	if err := db.Where("channel_id = ? AND model_name = ?", 15, "still-watched").First(&before).Error; err != nil {
		t.Fatalf("load observation: %v", err)
	}

	if _, _, err := FixAbility(); err != nil {
		t.Fatalf("FixAbility: %v", err)
	}
	roles := observedRoles(t, db, 15)
	if roles[ChannelLaneAgent+"|went-live"] != AbilityRoleBackup ||
		roles[ChannelLaneAgent+"|still-watched"] != AbilityRoleInventory {
		t.Fatalf("roles after repair = %+v", roles)
	}
	var rows []RouteObservation
	if err := db.Where("channel_id = ?", 15).Find(&rows).Error; err != nil {
		t.Fatalf("load observations: %v", err)
	}
	if len(rows) != 1 || rows[0].ModelName != "still-watched" || !rows[0].StartedAt.Equal(before.StartedAt) {
		t.Fatalf("observations after repair = %+v, want still-watched with its original start", rows)
	}
}

// An observation only counts while its route is still inventory: moving the
// route by any other path ends it.
func TestLoadActiveRouteObservationsRequiresInventory(t *testing.T) {
	db := setupRouteObservationTestDB(t)
	if err := db.Create(&[]Ability{
		{Group: ChannelLaneAgent, Model: "parked", ChannelId: 21, Enabled: true, Role: AbilityRoleInventory},
		{Group: ChannelLaneAgent, Model: "serving", ChannelId: 21, Enabled: true, Role: AbilityRolePrimary},
	}).Error; err != nil {
		t.Fatalf("seed abilities: %v", err)
	}
	if err := StartRouteObservations(nil, []RouteObservationCell{
		{Lane: ChannelLaneAgent, ModelName: "parked", ChannelId: 21},
		{Lane: ChannelLaneAgent, ModelName: "serving", ChannelId: 21},
	}, time.Now()); err != nil {
		t.Fatalf("start: %v", err)
	}
	watched, err := LoadActiveRouteObservations([]int{21})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(watched) != 1 || watched[0].ModelName != "parked" {
		t.Fatalf("watched = %+v, want only the parked route", watched)
	}
}

// Probe tallies and slices count only real verdicts.
func TestRouteProbeTalliesIgnoreSkippedProbes(t *testing.T) {
	db := setupRouteObservationTestDB(t)
	now := time.Now().UTC().Truncate(time.Hour)
	probes := []ChannelProbeResult{
		{Lane: ChannelLaneAgent, ModelName: "m", ChannelId: 31, ProbedAt: now.Add(-3 * time.Hour), Success: true, LatencyMs: 100},
		{Lane: ChannelLaneAgent, ModelName: "m", ChannelId: 31, ProbedAt: now.Add(-2 * time.Hour), Success: false, LatencyMs: 300},
		{Lane: ChannelLaneAgent, ModelName: "m", ChannelId: 31, ProbedAt: now.Add(-1 * time.Hour), Skipped: true},
	}
	if err := db.Create(&probes).Error; err != nil {
		t.Fatalf("seed probes: %v", err)
	}
	tallies, err := LoadRouteProbeTallies([]int{31}, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("tallies: %v", err)
	}
	tally := tallies[ChannelHealthCellKey("m", ChannelLaneAgent, 31)]
	if tally.Samples != 2 || tally.Failures != 1 || tally.AvgLatencyMs != 200 {
		t.Fatalf("tally = %+v, want 2 samples, 1 failure, 200ms", tally)
	}
	buckets, err := LoadRouteProbeBuckets([]int{31}, now.Add(-24*time.Hour), 3600)
	if err != nil {
		t.Fatalf("buckets: %v", err)
	}
	var samples int64
	for _, bucket := range buckets {
		samples += bucket.Samples
	}
	if len(buckets) != 2 || samples != 2 {
		t.Fatalf("buckets = %+v, want two one-probe slices", buckets)
	}
}
