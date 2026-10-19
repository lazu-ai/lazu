package middleware

import (
	"net/http"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestAdminAuthRejectsSessionIssuedBeforeRoleDemotion(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Id:             7601,
		Username:       "demoted-session-admin",
		Password:       "placeholder-hashed-password-value",
		Role:           common.RoleRootUser,
		Status:         common.UserStatusEnabled,
		SessionVersion: 6,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	// The last enabled administrator cannot be demoted; keep another one.
	standing := model.User{Id: 7602, Username: "standing-admin", Password: "placeholder-hashed-password-value", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	if err := model.CoreDB().Create(&standing).Error; err != nil {
		t.Fatalf("seed standing admin: %v", err)
	}
	oldSession := map[string]any{
		"username":        user.Username,
		"role":            user.Role,
		"id":              user.Id,
		"status":          user.Status,
		"session_version": user.SessionVersion,
	}

	demoted := user
	demoted.Role = common.RoleCommonUser
	if err := demoted.EditAuthorized(false, common.RoleRootUser, user.Role); err != nil {
		t.Fatalf("demote admin: %v", err)
	}
	if demoted.SessionVersion != user.SessionVersion+1 {
		t.Fatalf("demotion did not advance session version: got=%d want=%d", demoted.SessionVersion, user.SessionVersion+1)
	}

	w, payload := performAuthRequest(t, RootAuth(), oldSession, map[string]string{
		"X-Lazu-User": "7601",
	})
	if w.Code != http.StatusUnauthorized || consoleErrorCode(payload) != "session_expired" {
		t.Fatalf("pre-demotion admin session was accepted: status=%d payload=%v", w.Code, payload)
	}
}
