package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

// The health grid must show exactly what the plaza sells: enabled abilities on
// enabled channels. Stale rows and paused channels are configuration history,
// not routes.
func TestLoadChannelHealthGridOnlyLiveRoutes(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	channels := []Channel{
		{Id: 1, Name: "live", Type: 1, Status: common.ChannelStatusEnabled, Group: ChannelLaneDirect, Models: "gpt-5.2"},
		{Id: 2, Name: "paused", Type: 1, Status: common.ChannelStatusManuallyDisabled, Group: ChannelLaneDirect, Models: "gpt-5.2"},
	}
	for i := range channels {
		if err := db.Create(&channels[i]).Error; err != nil {
			t.Fatalf("seed channel: %v", err)
		}
	}
	rows := []Ability{
		{Group: ChannelLaneDirect, Model: "gpt-5.2", ChannelId: 1, Enabled: true},
		{Group: ChannelLaneDirect, Model: "deepseek-v3", ChannelId: 1, Enabled: false},
		{Group: ChannelLaneDirect, Model: "gpt-5.2", ChannelId: 2, Enabled: false},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}

	grid, err := LoadChannelHealthGrid()
	if err != nil {
		t.Fatalf("LoadChannelHealthGrid: %v", err)
	}
	if len(grid) != 1 || grid[0].ModelName != "gpt-5.2" || grid[0].ChannelId != 1 {
		t.Fatalf("grid = %+v, want only the live gpt-5.2 route on channel 1", grid)
	}
}

// A trailing comma or padded name in the model list must not turn into an
// ability row; UpdateAbilities has to clean input the same way AddAbilities does.
func TestUpdateAbilitiesTrimsModelNames(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	channel := Channel{Id: 7, Name: "jina", Type: 1, Status: common.ChannelStatusEnabled,
		Group: ChannelLaneDirect + ",bogus-lane", Models: " jina-reranker , ,jina-embed,"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channel.UpdateAbilities(nil); err != nil {
		t.Fatalf("UpdateAbilities: %v", err)
	}
	var rows []Ability
	if err := db.Where("channel_id = ?", 7).Order("model").Find(&rows).Error; err != nil {
		t.Fatalf("load abilities: %v", err)
	}
	if len(rows) != 2 || rows[0].Model != "jina-embed" || rows[1].Model != "jina-reranker" ||
		rows[0].Group != ChannelLaneDirect || rows[1].Group != ChannelLaneDirect {
		t.Fatalf("abilities = %+v, want two trimmed direct-lane rows", rows)
	}
}

// The audit rebuilds only channels whose rows drifted from their model list,
// and keeps the operator's per-route weight while doing so.
func TestAuditAbilitiesRebuildsDriftedChannels(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	channels := []Channel{
		{Id: 1, Name: "clean", Type: 1, Status: common.ChannelStatusEnabled, Group: ChannelLaneDirect, Models: "gpt-5.2"},
		{Id: 2, Name: "drifted", Type: 1, Status: common.ChannelStatusEnabled, Group: ChannelLaneDirect, Models: "deepseek-v4-pro"},
	}
	for i := range channels {
		if err := db.Create(&channels[i]).Error; err != nil {
			t.Fatalf("seed channel: %v", err)
		}
	}
	rows := []Ability{
		{Group: ChannelLaneDirect, Model: "gpt-5.2", ChannelId: 1, Enabled: true, Weight: 5},
		{Group: ChannelLaneDirect, Model: "deepseek-v4-pro", ChannelId: 2, Enabled: true, Weight: 9},
		{Group: ChannelLaneDirect, Model: "deepseek-v3", ChannelId: 2, Enabled: false},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}

	result, err := AuditAbilities()
	if err != nil {
		t.Fatalf("AuditAbilities: %v", err)
	}
	if result.Channels != 2 || result.Rebuilt != 1 || len(result.RebuiltID) != 1 || result.RebuiltID[0] != 2 {
		t.Fatalf("result = %+v, want exactly channel 2 rebuilt", result)
	}
	var after []Ability
	if err := db.Where("channel_id = ?", 2).Find(&after).Error; err != nil {
		t.Fatalf("load abilities: %v", err)
	}
	if len(after) != 1 || after[0].Model != "deepseek-v4-pro" || after[0].Weight != 9 {
		t.Fatalf("channel 2 abilities = %+v, want the live route with its weight kept", after)
	}
	var untouched int64
	db.Model(&Ability{}).Where("channel_id = ? AND weight = ?", 1, 5).Count(&untouched)
	if untouched != 1 {
		t.Fatalf("channel 1 should be untouched")
	}
}

// What we list and what we will serve have to be the same set: a model whose
// only supply is parked inventory must not appear in the catalogue, or every
// request for it is accepted by the plaza and refused by the router.
func TestInventoryLeavesTheCatalogueAndTheRouter(t *testing.T) {
	db := setupChannelHealthTestDB(t)
	channel := Channel{Id: 1, Name: "spare", Type: 1, Status: common.ChannelStatusEnabled,
		Group: ChannelLaneDirect, Models: "parked-model,live-model"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	rows := []Ability{
		{Group: ChannelLaneDirect, Model: "parked-model", ChannelId: 1, Enabled: true, Role: AbilityRoleInventory},
		{Group: ChannelLaneDirect, Model: "live-model", ChannelId: 1, Enabled: true, Role: AbilityRolePrimary},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}

	listed := GetGroupEnabledModels(ChannelLaneDirect)
	for _, name := range listed {
		if name == "parked-model" {
			t.Fatalf("inventory reached the catalogue: %v", listed)
		}
	}
	if len(listed) != 1 || listed[0] != "live-model" {
		t.Fatalf("listed = %v, want only the served model", listed)
	}

	// The grid keeps it, because an operator has to be able to see the spare
	// capacity they are holding and how it is doing.
	grid, err := LoadChannelHealthGrid()
	if err != nil {
		t.Fatalf("LoadChannelHealthGrid: %v", err)
	}
	if len(grid) != 2 {
		t.Fatalf("grid = %+v, want inventory to stay visible to operators", grid)
	}
}
