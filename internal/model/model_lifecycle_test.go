package model

import (
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestIsRetiredModelNameMatchesOnlyLegacyFamilies(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "gpt-3.5-turbo", want: true},
		{name: "gpt-4", want: true},
		{name: "gpt-4-0613", want: true},
		{name: "gpt-4-turbo-preview", want: true},
		{name: "gpt-4.5-preview", want: true},
		{name: "claude-3.5-sonnet-20241022", want: true},
		{name: "claude-3-7-sonnet-20250219", want: true},
		{name: "gemini-1.5-pro", want: true},
		{name: "gemini-2.0-flash", want: true},
		{name: "gemini-2.0-flash-lite-001", want: true},
		{name: "gemini-3-pro-preview", want: true},
		{name: "gemini-3.1-flash-lite-preview", want: true},
		{name: "gpt-audio-preview", want: true},
		{name: "gpt-realtime", want: true},
		{name: "computer-use-preview-2025-03-11", want: true},
		{name: "grok-2-vision", want: true},
		{name: "gpt-4.1-nano", want: true},
		{name: "gpt-4.1-nano-2025-04-14", want: true},
		{name: "gpt-4o-2024-05-13", want: true},
		{name: "gpt-5-2025-08-07", want: true},
		{name: "gpt-5-mini-2025-08-07", want: true},
		{name: "gpt-5-nano-2025-08-07", want: true},
		{name: "gpt-5-pro-2025-10-06", want: true},
		{name: "gpt-5.2", want: true},
		{name: "gpt-5.2-pro-2025-12-11", want: true},
		{name: "gpt-5.3-codex", want: true},
		{name: "claude-sonnet-4-5-20250929-thinking", want: true},
		{name: "claude-opus-4-5-20251101", want: true},
		{name: "gpt-4o", want: false},
		{name: "gpt-4.1", want: false},
		{name: "gpt-5.4", want: false},
		{name: "claude-sonnet-4-6", want: false},
		{name: "gemini-2.5-flash", want: false},
		{name: "gemini-3.1-pro-preview", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRetiredModelName(tc.name); got != tc.want {
				t.Fatalf("expected retired=%v, got %v", tc.want, got)
			}
		})
	}
}

func TestGetMissingModelsSkipsRetiredNames(t *testing.T) {
	previousDB := coreDB
	db, err := gorm.Open(gormsqlite.Open("file:missing_models_retired?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	EnsureColumnNamesInitialized()
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = previousDB
	}()

	if err := db.AutoMigrate(&Ability{}, &Model{}); err != nil {
		t.Fatalf("failed to migrate tables: %v", err)
	}
	if err := db.Create(&[]Ability{
		{Model: "gemini-3-pro-preview", Enabled: true},
		{Model: "live-new-model", Enabled: true},
	}).Error; err != nil {
		t.Fatalf("failed to seed abilities: %v", err)
	}

	missing, err := GetMissingModels()
	if err != nil {
		t.Fatalf("GetMissingModels returned error: %v", err)
	}
	if len(missing) != 1 || missing[0] != "live-new-model" {
		t.Fatalf("expected only live-new-model to be missing, got %#v", missing)
	}
}

func TestModelLifecyclePublicListable(t *testing.T) {
	tests := []struct {
		name  string
		model Model
		want  bool
	}{
		{name: "active", model: Model{ModelName: "gpt-4o", Status: 1, LifecycleStatus: ModelLifecycleActive}, want: true},
		{name: "candidate", model: Model{ModelName: "gpt-4o", Status: 1, LifecycleStatus: ModelLifecycleCandidate}, want: false},
		{name: "unknown", model: Model{ModelName: "gpt-4o", Status: 1, LifecycleStatus: "legacy-hidden"}, want: false},
		{name: "blank defaults to active", model: Model{ModelName: "gpt-4o", Status: 1}, want: true},
		{name: "retired", model: Model{ModelName: "gpt-4o", Status: 1, LifecycleStatus: ModelLifecycleRetired}, want: false},
		{name: "disabled", model: Model{ModelName: "gpt-4o", Status: 0, LifecycleStatus: ModelLifecycleActive}, want: false},
		{name: "retired rule", model: Model{ModelName: "gpt-4", Status: 1, LifecycleStatus: ModelLifecycleActive}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.model.IsPubliclyListable(); got != tc.want {
				t.Fatalf("expected public=%v, got %v", tc.want, got)
			}
		})
	}
}

func TestModelUpdatePreservesSyncProvenance(t *testing.T) {
	previousDB := coreDB
	db, err := gorm.Open(gormsqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	coreDB = db
	EnsureColumnNamesInitialized()
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		coreDB = previousDB
	}()

	if err := db.AutoMigrate(&Model{}); err != nil {
		t.Fatalf("failed to migrate model table: %v", err)
	}
	original := &Model{
		ModelName:       "deepseek-v3.2",
		Status:          1,
		LifecycleStatus: ModelLifecycleCandidate,
		Source:          ModelSourceBasellm,
		LastSeenAt:      123456,
	}
	if err := original.Insert(); err != nil {
		t.Fatalf("failed to insert model: %v", err)
	}

	updatePayload := &Model{
		Id:        original.Id,
		ModelName: original.ModelName,
		Status:    1,
	}
	if err := updatePayload.Update(); err != nil {
		t.Fatalf("failed to update model: %v", err)
	}

	var got Model
	if err := db.First(&got, "id = ?", original.Id).Error; err != nil {
		t.Fatalf("failed to reload model: %v", err)
	}
	if got.Source != ModelSourceBasellm {
		t.Fatalf("expected source %q, got %q", ModelSourceBasellm, got.Source)
	}
	if got.LastSeenAt != 123456 {
		t.Fatalf("expected last_seen_at to be preserved, got %d", got.LastSeenAt)
	}
	if got.LifecycleStatus != ModelLifecycleCandidate {
		t.Fatalf("expected lifecycle %q, got %q", ModelLifecycleCandidate, got.LifecycleStatus)
	}
}
