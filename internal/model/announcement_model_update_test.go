package model

import (
	"testing"
	"time"
)

// The editor sends model-update metadata on every save; a round trip has to
// come back with the same models and day, and a kind the console recognises.
func TestUpsertAnnouncementStoresModelUpdateFields(t *testing.T) {
	db := setupAnnouncementTestDB(t)
	effective := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	row, err := upsertAnnouncementWithDB(db, AnnouncementUpsert{
		Kind:              AnnouncementKindModelUpdate,
		TitleI18N:         map[string]string{"zh": "gpt-4o-mini 下线"},
		BodyI18N:          map[string]any{"zh": []string{"上游停止服务。"}},
		DefaultLocale:     "zh",
		AffectedModels:    []string{"gpt-4o-mini", " gpt-4o-mini ", "gpt-4.1-mini"},
		AffectedModelsSet: true,
		EffectiveDate:     &effective,
		EffectiveDateSet:  true,
		Published:         true,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if row.Kind != AnnouncementKindModelUpdate {
		t.Fatalf("kind = %q, want %q", row.Kind, AnnouncementKindModelUpdate)
	}

	view := row.ToView("zh", nil)
	// Duplicates and padding are normalised away so the console never renders
	// the same model chip twice.
	if len(view.AffectedModels) != 2 ||
		view.AffectedModels[0] != "gpt-4o-mini" ||
		view.AffectedModels[1] != "gpt-4.1-mini" {
		t.Fatalf("affected models = %#v", view.AffectedModels)
	}
	if view.EffectiveDate == nil || !view.EffectiveDate.Equal(effective) {
		t.Fatalf("effective date = %#v, want %v", view.EffectiveDate, effective)
	}
}

// An edit that leaves the fields out (the deploy pipeline, or a kind switch
// away from model_update) must not wipe what is already stored.
func TestUpsertAnnouncementKeepsModelFieldsWhenUnset(t *testing.T) {
	db := setupAnnouncementTestDB(t)
	effective := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	row, err := upsertAnnouncementWithDB(db, AnnouncementUpsert{
		Kind:              AnnouncementKindModelUpdate,
		TitleI18N:         map[string]string{"zh": "标题"},
		AffectedModels:    []string{"gpt-4o-mini"},
		AffectedModelsSet: true,
		EffectiveDate:     &effective,
		EffectiveDateSet:  true,
		Published:         true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := upsertAnnouncementWithDB(db, AnnouncementUpsert{
		ID:        row.ID,
		Kind:      AnnouncementKindModelUpdate,
		TitleI18N: map[string]string{"zh": "改过的标题"},
		Published: true,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	view := updated.ToView("zh", nil)
	if len(view.AffectedModels) != 1 || view.AffectedModels[0] != "gpt-4o-mini" {
		t.Fatalf("affected models were clobbered: %#v", view.AffectedModels)
	}
	if view.EffectiveDate == nil {
		t.Fatalf("effective date was cleared")
	}
}

func TestNormalizeAnnouncementKindAcceptsConsoleKinds(t *testing.T) {
	for _, kind := range []string{
		AnnouncementKindReleaseNote,
		AnnouncementKindOperational,
		AnnouncementKindMaintenance,
		AnnouncementKindIncident,
		AnnouncementKindModelUpdate,
	} {
		if got := normalizeAnnouncementKind(kind); got != kind {
			t.Fatalf("normalizeAnnouncementKind(%q) = %q", kind, got)
		}
	}
	if got := normalizeAnnouncementKind("nonsense"); got != AnnouncementKindReleaseNote {
		t.Fatalf("unknown kind = %q, want release_note fallback", got)
	}
}
