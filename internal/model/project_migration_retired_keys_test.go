package model

import (
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// The one-time normalization that retires the ownerless-Key compatibility
// shim: a row written as a service Key with no owner gets its project's owner,
// a row whose project has no owner to give is left exactly as it is, and a
// second run changes nothing.
func TestNormalizeRetiredServiceKeyOwnersAttributesRowsOnce(t *testing.T) {
	db := setupProjectTestDB(t, true)
	addLegacyTokenIsServiceColumn(t, db)
	owner := User{Username: "normalize-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	// A project nobody owns, and one that does not exist at all.
	stranger := User{Username: "normalize-stranger"}
	if err := db.Create(&stranger).Error; err != nil {
		t.Fatal(err)
	}
	var ownerless Project
	if err := db.Where("owner_user_id = ?", stranger.Id).First(&ownerless).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("projects").Where("id = ?", ownerless.ID).Update("owner_user_id", 0).Error; err != nil {
		t.Fatal(err)
	}

	rows := map[string]*Token{
		"retired": {ProjectID: project.ID, Name: "retired", Key: strings.Repeat("a", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1},
		"orphan":  {ProjectID: ownerless.ID, Name: "orphan", Key: strings.Repeat("b", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1},
		"missing": {ProjectID: 909090, Name: "missing", Key: strings.Repeat("c", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1},
		"owned":   {ProjectID: project.ID, UserId: owner.Id, Name: "owned", Key: strings.Repeat("d", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1},
	}
	for name, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
		if name == "owned" {
			continue
		}
		if err := db.Model(&Token{}).Where("id = ?", row.Id).Updates(map[string]any{"user_id": 0, "is_service": true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// A row that was never a service Key is not this migration's business.
	plain := Token{ProjectID: project.ID, Name: "plain", Key: strings.Repeat("e", 48), Status: common.TokenStatusEnabled, ExpiredTime: -1}
	if err := db.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&Token{}).Where("id = ?", plain.Id).Updates(map[string]any{"user_id": 0, "is_service": false}).Error; err != nil {
		t.Fatal(err)
	}

	want := map[int]int{
		rows["retired"].Id: owner.Id,
		rows["orphan"].Id:  0,
		rows["missing"].Id: 0,
		rows["owned"].Id:   owner.Id,
		plain.Id:           0,
	}
	for run := 0; run < 2; run++ {
		if err := normalizeRetiredServiceKeyOwners(db); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for id, expected := range want {
			var got Token
			if err := db.Select("id", "user_id").First(&got, id).Error; err != nil {
				t.Fatal(err)
			}
			if got.UserId != expected {
				t.Fatalf("run %d: token %d has user_id %d, want %d", run, id, got.UserId, expected)
			}
		}
	}
}

// The normal case: a database with nothing to normalize is untouched.
func TestNormalizeRetiredServiceKeyOwnersIsANoOpWithoutRetiredRows(t *testing.T) {
	db := setupProjectTestDB(t, true)
	owner := User{Username: "no-op-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := db.Model(&Token{}).Where("user_id = ?", owner.Id).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := normalizeRetiredServiceKeyOwners(db); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := db.Model(&Token{}).Where("user_id = ?", owner.Id).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("no-op run changed attribution: %d -> %d", before, after)
	}
}

// Normalizing the Key alone would be worse than doing nothing: the budget sums
// read the actor, so the owner's month would ignore the Key's own earlier
// charges and its pending holds and let the next request through past the cap.
// Everything the Key already produced moves with it.
func TestNormalizeRetiredServiceKeyOwnersMovesWhatTheKeyProduced(t *testing.T) {
	db := setupProjectTestDB(t, true)
	if err := db.AutoMigrate(&ProjectSpend{}, &SpendBudgetReservation{}, &File{}, &MediaJob{}, &Log{}); err != nil {
		t.Fatal(err)
	}
	addLegacyTokenIsServiceColumn(t, db)
	owner := User{Username: "reattribute-owner"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	var project Project
	if err := db.Where("owner_user_id = ?", owner.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	key, err := common.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	retired := Token{Name: "retired", Key: key, ProjectID: project.ID, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	if err := retired.Insert(); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&Token{}).Where("id = ?", retired.Id).Updates(map[string]any{"user_id": 0, "is_service": true}).Error; err != nil {
		t.Fatal(err)
	}

	// One row per table the Key's history lives in, plus a row on another Key
	// that must not move.
	retiredID := retired.Id
	otherKeyID := retired.Id + 999
	now := time.Now().UTC()
	for _, seed := range []any{
		&ProjectSpend{ProjectID: project.ID, TokenID: retired.Id, ActorUserID: 0, PayerUserID: owner.Id, RequestID: "spend-1", EventKey: "spend-1", AmountMicrousd: 5, OccurredAt: now},
		&ProjectSpend{ProjectID: project.ID, TokenID: otherKeyID, ActorUserID: 0, PayerUserID: owner.Id, RequestID: "other-key", EventKey: "other-key", AmountMicrousd: 5, OccurredAt: now},
		&SpendBudgetReservation{ProjectID: project.ID, TokenID: retired.Id, ActorUserID: 0, PayerUserID: owner.Id, RequestID: "hold-1", ReservedMicrousd: 5, CreatedAt: now},
		&File{ID: "file-1", ProjectID: project.ID, TokenID: &retiredID, ActorUserID: 0, UserID: owner.Id, Filename: "f.bin", MimeType: "application/octet-stream", Purpose: "assistants", Bytes: 1, StorageKey: "k", CreatedAt: now.Unix()},
		&MediaJob{PublicID: "mj-1", ProjectID: project.ID, TokenID: &retiredID, ActorUserID: 0, UserID: owner.Id, ModelName: "m", Kind: MediaJobKindVideo, Status: MediaJobStatusRunning},
		&Log{ProjectID: project.ID, TokenId: retired.Id, UserId: 0, CreatedAt: now.Unix()},
	} {
		if err := db.Create(seed).Error; err != nil {
			t.Fatalf("seed %T: %v", seed, err)
		}
	}

	for run := 0; run < 2; run++ {
		if err := normalizeRetiredServiceKeyOwners(db); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		for _, probe := range []struct{ table, actor string }{
			{"project_spends", "actor_user_id"},
			{"spend_budget_reservations", "actor_user_id"},
			{"files", "actor_user_id"},
			{"media_jobs", "actor_user_id"},
			{"logs", "user_id"},
		} {
			var moved int64
			if err := db.Table(probe.table).Where("token_id = ? AND "+probe.actor+" = ?", retired.Id, owner.Id).Count(&moved).Error; err != nil {
				t.Fatal(err)
			}
			if moved != 1 {
				t.Fatalf("run %d: %s did not move its row to the owner", run, probe.table)
			}
		}
		var untouched int64
		if err := db.Table("project_spends").Where("token_id = ? AND actor_user_id = ?", otherKeyID, 0).Count(&untouched).Error; err != nil {
			t.Fatal(err)
		}
		if untouched != 1 {
			t.Fatalf("run %d: another Key's row was reattributed", run)
		}
	}
}

// addLegacyTokenIsServiceColumn recreates tokens.is_service, which schema
// version 15 drops after the retired-Key migration has read it. The migration
// still runs on databases that predate the drop.
func addLegacyTokenIsServiceColumn(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasColumn("tokens", "is_service") {
		return
	}
	if err := db.Exec("ALTER TABLE tokens ADD COLUMN is_service numeric NOT NULL DEFAULT false").Error; err != nil {
		t.Fatal(err)
	}
}
