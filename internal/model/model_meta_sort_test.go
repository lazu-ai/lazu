package model

import (
	"strings"
	"testing"
)

func TestModelSortOrderAllowlist(t *testing.T) {
	tests := map[string]string{
		"name_asc":     "models.model_name ASC, models.id DESC",
		"name_desc":    "models.model_name DESC, models.id DESC",
		"created_asc":  "models.created_time ASC, models.id ASC",
		"created_desc": "models.created_time DESC, models.id DESC",
		"updated_asc":  "models.updated_time ASC, models.id ASC",
		"updated_desc": "models.updated_time DESC, models.id DESC",
		"status_asc":   "models.status ASC, models.updated_time DESC, models.id DESC",
		"status_desc":  "models.status DESC, models.updated_time DESC, models.id DESC",
	}

	for sortBy, expected := range tests {
		if got := modelSortOrder(sortBy); got != expected {
			t.Fatalf("sort %q: expected %q, got %q", sortBy, expected, got)
		}
	}
}

func TestModelSortOrderRejectsRawSQL(t *testing.T) {
	got := modelSortOrder("updated_desc; DROP TABLE models")
	if got != "models.created_time DESC, models.id DESC" {
		t.Fatalf("expected unsafe sort to fall back to default, got %q", got)
	}
	if strings.Contains(got, ";") {
		t.Fatalf("sort order must not contain raw SQL separator: %q", got)
	}
}
