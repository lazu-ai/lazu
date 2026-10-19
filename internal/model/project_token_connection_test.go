package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

func TestProjectTokenConnectionPreservesConcurrentUsage(t *testing.T) {
	db := setupProjectTestDB(t, true)
	user := User{Username: "connection-owner"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := Token{UserId: user.Id, Key: strings.Repeat("a", 48), Name: "keep name", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000, VendorGroups: TokenVendorGroupMap{"1": "cheap"}}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	stale := token
	// Settlement does not bump project_version. Reproduce it after the handler read.
	if err := db.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{"remain_quota": gorm.Expr("remain_quota - ?", 100), "used_quota": gorm.Expr("used_quota + ?", 100), "status": common.TokenStatusDisabled}).Error; err != nil {
		t.Fatal(err)
	}
	stale.Product = "codex"
	stale.Name = "must not save"
	stale.VendorGroups = nil
	if err := UpdateProjectTokenConnection(token.ProjectID, user.Id, &stale, true, false, false); err != nil {
		t.Fatal(err)
	}
	var saved Token
	if err := db.First(&saved, token.Id).Error; err != nil {
		t.Fatal(err)
	}
	if saved.RemainQuota != 900 || saved.UsedQuota != 100 || saved.Status != common.TokenStatusDisabled || saved.Name != token.Name || saved.VendorGroups["1"] != "cheap" || saved.Product != "codex" {
		t.Fatalf("connection save changed unrelated fields: %+v", saved)
	}
	if stale.RemainQuota != 900 || stale.ProjectVersion != token.ProjectVersion+1 {
		t.Fatalf("response is stale: %+v", stale)
	}
	// Explicit empty policy clears lanes, while an omitted product stays intact.
	stale.VendorGroups = TokenVendorGroupMap{}
	stale.Product = "claude-code"
	if err := UpdateProjectTokenConnection(token.ProjectID, user.Id, &stale, false, true, false); err != nil {
		t.Fatal(err)
	}
	if len(stale.VendorGroups) != 0 || stale.Product != "codex" {
		t.Fatalf("partial update failed: %+v", stale)
	}
	token.Product = "claude-code"
	if err := UpdateProjectTokenConnection(token.ProjectID, user.Id, &token, true, false, false); !errors.Is(err, ErrProjectVersionConflict) {
		t.Fatalf("stale policy should conflict: %v", err)
	}
	outsider := User{Username: "outsider"}
	if err := db.Create(&outsider).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpdateProjectTokenConnection(token.ProjectID, outsider.Id, &stale, true, false, false); err == nil {
		t.Fatal("outsider changed key")
	}
}
