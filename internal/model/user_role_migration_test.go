package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

func TestMigrateRetiredUserRolesCollapsesToTwoRoles(t *testing.T) {
	db := setupProjectTestDB(t, false)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	users := []User{
		{Username: "guest", Role: 0, Status: common.UserStatusEnabled, SessionVersion: 2},
		{Username: "customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SessionVersion: 2},
		{Username: "old-admin", Role: 10, Status: common.UserStatusEnabled, SessionVersion: 2},
		{Username: "root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, SessionVersion: 2},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// Role 0 is the column's zero value; force it past GORM's default.
	if err := db.Model(&User{}).Where("id = ?", users[0].Id).Update("role", 0).Error; err != nil {
		t.Fatalf("seed guest role: %v", err)
	}

	for run := 0; run < 2; run++ {
		if err := migrateRetiredUserRoles(db); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}

	want := map[string]struct{ role, version int }{
		"guest":     {common.RoleCommonUser, 3},
		"customer":  {common.RoleCommonUser, 2},
		"old-admin": {common.RoleRootUser, 3},
		"root":      {common.RoleRootUser, 2},
	}
	var stored []User
	if err := db.Find(&stored).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, user := range stored {
		expected, ok := want[user.Username]
		if !ok {
			continue
		}
		if user.Role != expected.role || user.SessionVersion != expected.version {
			t.Fatalf("%s: role=%d version=%d, want role=%d version=%d", user.Username, user.Role, user.SessionVersion, expected.role, expected.version)
		}
	}
}
