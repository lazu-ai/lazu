package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

// Both endpoints must answer the same way whether or not the address is
// registered. A difference here is a free account-enumeration oracle.
func TestPasswordResetRequestDoesNotRevealAccountExistence(t *testing.T) {
	setupOIDCTestDB(t)
	defer waitForAuthEmailDeliveries()
	gin.SetMode(gin.TestMode)

	existing := model.User{
		Username: "reset-known",
		Password: "password123",
		Email:    "known@example.com",
		Status:   common.UserStatusEnabled,
	}
	if err := existing.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	engine := gin.New()
	engine.GET("/reset_password", SendPasswordResetEmail)

	known := httptest.NewRecorder()
	engine.ServeHTTP(known, httptest.NewRequest(http.MethodGet, "/reset_password?email=known@example.com", nil))
	unknown := httptest.NewRecorder()
	engine.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/reset_password?email=nobody@example.com", nil))

	if known.Body.String() != unknown.Body.String() {
		t.Fatalf("password reset response leaks account existence: known=%s unknown=%s", known.Body.String(), unknown.Body.String())
	}
	if !strings.Contains(known.Body.String(), `"success":true`) {
		t.Fatalf("expected a constant success response, got %s", known.Body.String())
	}
}

func TestVerificationRequestDoesNotRevealAccountExistence(t *testing.T) {
	setupOIDCTestDB(t)
	defer waitForAuthEmailDeliveries()
	gin.SetMode(gin.TestMode)

	existing := model.User{
		Username: "verify-known",
		Password: "password123",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}
	if err := existing.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	engine := gin.New()
	engine.GET("/verification", SendEmailVerification)

	taken := httptest.NewRecorder()
	engine.ServeHTTP(taken, httptest.NewRequest(http.MethodGet, "/verification?email=taken@example.com", nil))
	unknown := httptest.NewRecorder()
	engine.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/verification?email=unknown@example.com", nil))

	if !strings.Contains(taken.Body.String(), `"success":true`) {
		t.Fatalf("taken address must not be reported back to the requester, got %s", taken.Body.String())
	}
	if taken.Body.String() != unknown.Body.String() {
		t.Fatalf("verification response leaks account existence: taken=%s unknown=%s", taken.Body.String(), unknown.Body.String())
	}
	if strings.Contains(taken.Body.String(), "已被占用") {
		t.Fatalf("response still names the address as taken: %s", taken.Body.String())
	}
}

func TestResetUserPasswordTokenBindsCanonicalOwnerAndSessionVersion(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisVerification := common.RedisVerificationCodeEnabled
	previousRedisEnabled := common.RedisEnabled
	common.RedisVerificationCodeEnabled = false
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisVerificationCodeEnabled = previousRedisVerification
		common.RedisEnabled = previousRedisEnabled
	})
	user := model.User{
		Username: "reset-case",
		Password: "password123",
		Email:    "Canonical@example.com",
		Status:   common.UserStatusEnabled,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	firstToken := "first-reset-token"
	firstKey := model.PasswordResetVerificationKey(user.Id, user.SessionVersion)
	common.RegisterVerificationCodeWithKey(firstKey, firstToken, common.PasswordResetPurpose)
	if _, err := model.ResetUserPasswordWithToken("canonical@EXAMPLE.com", user.Id, user.SessionVersion, firstToken, "replacement-password"); err != nil {
		t.Fatalf("case-insensitive reset failed: %v", err)
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload password: %v", err)
	}
	if !common.ValidatePasswordAndHash("replacement-password", stored.Password) {
		t.Fatal("password was not updated for the canonical account")
	}
	if stored.SessionVersion != 1 || stored.NeedsPassword {
		t.Fatalf("reset should revoke old sessions and enable password login: version=%d needs_password=%t", stored.SessionVersion, stored.NeedsPassword)
	}
	staleProfile := stored
	staleProfile.NeedsPassword = true
	secondToken := "second-reset-token"
	secondKey := model.PasswordResetVerificationKey(stored.Id, stored.SessionVersion)
	common.RegisterVerificationCodeWithKey(secondKey, secondToken, common.PasswordResetPurpose)
	secondReset, err := model.ResetUserPasswordWithToken("canonical@example.com", stored.Id, stored.SessionVersion, secondToken, "second-replacement-password")
	if err != nil {
		t.Fatalf("second reset failed: %v", err)
	}
	if secondReset.SessionVersion != 2 {
		t.Fatalf("second reset did not advance session version: %d", secondReset.SessionVersion)
	}
	staleProfile.DisplayName = "updated from stale profile"
	if err := staleProfile.Update(false); err != nil {
		t.Fatalf("stale profile update failed: %v", err)
	}
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload after stale profile update: %v", err)
	}
	if stored.SessionVersion != 2 {
		t.Fatalf("stale profile rolled session version back to %d", stored.SessionVersion)
	}
	if !common.ValidatePasswordAndHash("second-replacement-password", stored.Password) {
		t.Fatal("stale profile restored the previous password")
	}
	if stored.NeedsPassword {
		t.Fatal("stale profile restored needs_password and reopened the first-password bypass")
	}
	if _, err := model.ResetUserPasswordWithToken("canonical@example.com", stored.Id, stored.SessionVersion, secondToken, "third-password"); err == nil {
		t.Fatal("consumed token must not be reusable at the new session version")
	}
	if _, err := model.ResetUserPasswordWithToken("missing@example.com", 999999, 0, "missing-token", "replacement-password"); err == nil {
		t.Fatal("missing account must not report a successful password update")
	}
}

// A social account holds a random placeholder password, so its owner must be
// able to choose a first one without producing the "original" they never saw.
func TestCheckUpdatePasswordAllowsFirstPasswordForSocialAccount(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username:      "social-user",
		Password:      "random-placeholder-value",
		Email:         "social@example.com",
		Status:        common.UserStatusEnabled,
		NeedsPassword: true,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	stored, err := model.GetUserById(user.Id, false)
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	staleProfile := *stored

	pending := model.User{
		Id:          user.Id,
		Username:    stored.Username,
		DisplayName: stored.DisplayName,
		Password:    "brand-new-password",
	}
	if _, err := pending.UpdateWithPasswordAndRevokeSessions(""); err != nil {
		t.Fatalf("set first password: %v", err)
	}
	refreshed, err := model.GetUserById(user.Id, false)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if refreshed.NeedsPassword {
		t.Fatal("needs_password should be cleared once a real password exists")
	}
	if refreshed.SessionVersion != 1 {
		t.Fatalf("first password should revoke old sessions, got version %d", refreshed.SessionVersion)
	}
	// A second concurrent-intent request can no longer reuse the first-password
	// exception after the locked transaction clears it.
	pending.Password = "another-password"
	if _, err := pending.UpdateWithPasswordAndRevokeSessions(""); err == nil {
		t.Fatal("expected the original-password check after the first password wins")
	}
	if _, err := pending.UpdateWithPasswordAndRevokeSessions("wrong-original"); err == nil {
		t.Fatal("expected the original-password check to be enforced after a password is set")
	}
	if _, err := pending.UpdateWithPasswordAndRevokeSessions("brand-new-password"); err != nil {
		t.Fatalf("correct original password should permit a later change: %v", err)
	}

	staleProfile.DisplayName = "stale social profile"
	staleProfile.NeedsPassword = true
	if err := staleProfile.Update(false); err != nil {
		t.Fatalf("write stale profile: %v", err)
	}
	if err := model.CoreDB().First(&refreshed, user.Id).Error; err != nil {
		t.Fatalf("reload after stale write: %v", err)
	}
	if refreshed.NeedsPassword {
		t.Fatal("stale settings/profile write restored needs_password")
	}
}

func TestConcurrentFirstPasswordOnlyOneRequestUsesTheException(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })
	rawDB, err := model.CoreDB().DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	// A single connection gives SQLite the same serialized row-lock behavior
	// that the production PostgreSQL FOR UPDATE clause provides.
	rawDB.SetMaxOpenConns(1)

	user := model.User{
		Username:      "concurrent-first-password",
		Password:      "random-placeholder-value",
		Email:         "concurrent-first@example.com",
		Status:        common.UserStatusEnabled,
		NeedsPassword: true,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, password := range []string{"first-concurrent-password", "second-concurrent-password"} {
		password := password
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pending := model.User{Id: user.Id, Password: password}
			_, err := pending.UpdateWithPasswordAndRevokeSessions("")
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	for result := range results {
		if result == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one first-password request to succeed, got %d", successes)
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.NeedsPassword || stored.SessionVersion != 1 {
		t.Fatalf("unexpected credential state after concurrent update: needs_password=%t version=%d", stored.NeedsPassword, stored.SessionVersion)
	}
}

func TestResetTokenStaysConsumedWhenDatabaseUpdateFails(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisVerification := common.RedisVerificationCodeEnabled
	previousRedisEnabled := common.RedisEnabled
	common.RedisVerificationCodeEnabled = false
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisVerificationCodeEnabled = previousRedisVerification
		common.RedisEnabled = previousRedisEnabled
	})

	user := model.User{Username: "reset-failure", Password: "password123", Email: "failure@example.com", Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "one-time-reset-token"
	verificationKey := model.PasswordResetVerificationKey(user.Id, user.SessionVersion)
	common.RegisterVerificationCodeWithKey(verificationKey, token, common.PasswordResetPurpose)

	callbackName := "test:fail-password-reset-update"
	if err := model.CoreDB().Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("forced password update failure"))
		}
	}); err != nil {
		t.Fatalf("register failure callback: %v", err)
	}
	if _, err := model.ResetUserPasswordWithToken(user.Email, user.Id, user.SessionVersion, token, "replacement-password"); err == nil {
		t.Fatal("expected forced database failure")
	}
	if err := model.CoreDB().Callback().Update().Remove(callbackName); err != nil {
		t.Fatalf("remove failure callback: %v", err)
	}
	if _, err := model.ResetUserPasswordWithToken(user.Email, user.Id, user.SessionVersion, token, "replacement-password"); err == nil {
		t.Fatal("database failure must not resurrect the consumed reset token")
	}
}

func TestSecurityChangesAdvanceSessionVersion(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username:       "managed-user",
		Password:       "password123",
		Email:          "managed@example.com",
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusEnabled,
		NeedsPassword:  true,
		SessionVersion: 4,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	seedStandingAdmin(t)

	adminEdit := model.User{
		Id:          user.Id,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Role:        user.Role,
		Status:      user.Status,
		Password:    "admin-replacement-password",
	}
	if err := adminEdit.Edit(true); err != nil {
		t.Fatalf("admin password edit: %v", err)
	}
	if adminEdit.SessionVersion != 5 || adminEdit.NeedsPassword {
		t.Fatalf("admin password edit did not revoke sessions: version=%d needs_password=%t", adminEdit.SessionVersion, adminEdit.NeedsPassword)
	}

	adminEdit.Role = common.RoleRootUser
	if err := adminEdit.Edit(false); err != nil {
		t.Fatalf("admin role edit: %v", err)
	}
	if adminEdit.SessionVersion != 6 {
		t.Fatalf("role edit did not revoke sessions: %d", adminEdit.SessionVersion)
	}

	// ManageUser's dedicated role/status transaction enforces the same boundary.
	disabled := common.UserStatusDisabled
	if _, err := model.UpdateUserRoleStatus(adminEdit.Id, common.RoleRootUser, adminEdit.Role, nil, &disabled); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload managed user: %v", err)
	}
	if stored.SessionVersion != 7 {
		t.Fatalf("status change did not revoke sessions: %d", stored.SessionVersion)
	}
}

func TestAuthorizedAdminEditRejectsConcurrentPromotion(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username:    "admin-edit-race",
		DisplayName: "Before",
		Password:    "password123",
		Email:       "admin-edit-race@example.com",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	staleEdit := user
	staleEdit.DisplayName = "Stale admin edit"
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleRootUser).Error; err != nil {
		t.Fatalf("concurrent promote: %v", err)
	}
	if err := staleEdit.EditAuthorized(false, common.RoleRootUser, common.RoleCommonUser); err == nil {
		t.Fatal("stale admin edit overwrote a concurrently promoted user")
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.Role != common.RoleRootUser || stored.DisplayName != "Before" {
		t.Fatalf("stale edit mutated promoted account: role=%d display=%q", stored.Role, stored.DisplayName)
	}
}

func TestStaleProfileAndSettingUpdatesCannotRestoreSecurityOrBillingState(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username:       "stale-profile-security",
		DisplayName:    "Original profile",
		Password:       "password123",
		Email:          "stale-security@example.com",
		Role:           common.RoleRootUser,
		Status:         common.UserStatusEnabled,
		Quota:          1000,
		SessionVersion: 4,
		Setting:        `{"notify_type":"email"}`,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	seedStandingAdmin(t)
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 1000).Error; err != nil {
		t.Fatalf("seed quota: %v", err)
	}
	if err := model.CoreDB().First(&user, user.Id).Error; err != nil {
		t.Fatalf("reload seeded user: %v", err)
	}
	staleProfile := user
	staleProfile.Setting = ""
	staleSetting := user
	staleSetting.Setting = `{"notify_type":"webhook"}`

	currentRole := common.RoleCommonUser
	currentStatus := common.UserStatusDisabled
	secured, err := model.UpdateUserRoleStatus(user.Id, common.RoleRootUser, user.Role, &currentRole, &currentStatus)
	if err != nil {
		t.Fatalf("apply security state: %v", err)
	}
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{
		"rpm_limit": 42,
		"quota":     25,
	}).Error; err != nil {
		t.Fatalf("apply billing state: %v", err)
	}

	staleProfile.DisplayName = "Fresh profile"
	if err := staleProfile.Update(false); err != nil {
		t.Fatalf("update stale profile intent: %v", err)
	}
	staleSetting.Username = "stale-username-must-not-win"
	if err := staleSetting.Update(false); err != nil {
		t.Fatalf("update stale setting intent: %v", err)
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.Role != currentRole || stored.Status != currentStatus || stored.RPMLimit != 42 || stored.Quota != 25 {
		t.Fatalf("stale update restored protected state: role=%d status=%d rpm_limit=%d quota=%d", stored.Role, stored.Status, stored.RPMLimit, stored.Quota)
	}
	if stored.SessionVersion != secured.SessionVersion {
		t.Fatalf("profile update unexpectedly changed session version: got=%d want=%d", stored.SessionVersion, secured.SessionVersion)
	}
	if stored.DisplayName != "Fresh profile" {
		t.Fatalf("explicit profile update was lost: %q", stored.DisplayName)
	}
	if stored.Username != user.Username {
		t.Fatalf("setting update overwrote username: %q", stored.Username)
	}
	if stored.Setting != staleSetting.Setting {
		t.Fatalf("explicit setting update was lost: %s", stored.Setting)
	}
}

func TestDedicatedUserFieldUpdatesPreserveExistingCallsites(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{Username: "field-update-user", Password: "password123", Email: "before@example.com", Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := model.UpdateUserAccessToken(user.Id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatalf("seed old access token: %v", err)
	}
	if err := model.UpdateUserAccessToken(user.Id, "12345678901234567890123456789012"); err != nil {
		t.Fatalf("update access token: %v", err)
	}
	if err := model.UpdateUserEmail(user.Id, "after@example.com"); err != nil {
		t.Fatalf("update email: %v", err)
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.GetAccessToken() != "12345678901234567890123456789012" || stored.Email != "after@example.com" {
		t.Fatalf("dedicated updates were not persisted: token=%q email=%q", stored.GetAccessToken(), stored.Email)
	}
	if model.ValidateAccessToken("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") != nil {
		t.Fatal("rotated access token remained valid")
	}
	if model.ValidateAccessToken("12345678901234567890123456789012") == nil {
		t.Fatal("new access token was not valid")
	}
}

func TestManageUserDeleteDoesNotUpdateTheSoftDeletedRow(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })
	gin.SetMode(gin.TestMode)

	user := model.User{Username: "managed-delete", Password: "password123", Email: "managed-delete@example.com", Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("role", common.RoleRootUser)
	c.Set("id", 999)
	c.Set("username", "root")
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(fmt.Sprintf(`{"id":%q,"action":"delete"}`, user.PublicID)))
	ManageUser(c)

	if !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("delete reported failure after committing soft delete: %s", w.Body.String())
	}
	var stored model.User
	if err := model.CoreDB().Unscoped().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload deleted user: %v", err)
	}
	if !stored.DeletedAt.Valid {
		t.Fatal("manage delete did not soft-delete the user")
	}
}

func TestDeleteUserWithExpectedRoleRejectsConcurrentPromotion(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{Username: "delete-role-race", Password: "password123", Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleRootUser).Error; err != nil {
		t.Fatalf("promote user: %v", err)
	}
	if _, err := model.DeleteUserWithExpectedRole(user.Id, common.RoleRootUser, common.RoleCommonUser); err == nil {
		t.Fatal("stale delete removed a concurrently promoted user")
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("promoted user was deleted: %v", err)
	}
	if stored.Role != common.RoleRootUser || stored.DeletedAt.Valid {
		t.Fatalf("unexpected user state after rejected delete: role=%d deleted=%t", stored.Role, stored.DeletedAt.Valid)
	}
}

func TestHardDeleteUserAuthorizedRejectsConcurrentPromotion(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username: "hard-delete-role-race",
		Password: "password123",
		Email:    "hard-delete-role-race@example.com",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	identity := model.UserIdentity{
		UserID:   user.Id,
		Provider: "test-provider",
		Subject:  "hard-delete-role-race-subject",
		Email:    user.Email,
	}
	if err := model.CoreDB().Create(&identity).Error; err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleRootUser).Error; err != nil {
		t.Fatalf("promote user: %v", err)
	}

	if _, err := model.HardDeleteUserAuthorized(user.Id, common.RoleRootUser, common.RoleCommonUser); err == nil {
		t.Fatal("stale hard delete removed a concurrently promoted user")
	}
	if _, err := model.HardDeleteUserAuthorized(user.Id, common.RoleRootUser, common.RoleRootUser); err == nil {
		t.Fatal("admin hard delete removed a locked peer account")
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("promoted user was hard-deleted: %v", err)
	}
	var identityCount int64
	if err := model.CoreDB().Model(&model.UserIdentity{}).Where("user_id = ?", user.Id).Count(&identityCount).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if stored.Role != common.RoleRootUser || identityCount != 1 {
		t.Fatalf("rejected hard delete mutated account: role=%d identities=%d", stored.Role, identityCount)
	}
}

func TestDeleteSelfUserRejectsConcurrentRootPromotion(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })

	user := model.User{
		Username: "self-delete-role-race",
		Password: "password123",
		Email:    "self-delete-role-race@example.com",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
	}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := model.CoreDB().Model(&model.User{}).Where("id = ?", user.Id).Update("role", common.RoleRootUser).Error; err != nil {
		t.Fatalf("promote user to root: %v", err)
	}

	if _, err := model.DeleteSelfUser(user.Id); !errors.Is(err, model.ErrCannotDeleteRootUser) {
		t.Fatalf("self-delete should reject the locked root role, got %v", err)
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("promoted root user was deleted: %v", err)
	}
	if stored.Role != common.RoleRootUser || stored.DeletedAt.Valid {
		t.Fatalf("unexpected root state after rejected self-delete: role=%d deleted=%t", stored.Role, stored.DeletedAt.Valid)
	}
}

func TestResetPasswordV2InvalidTokenDoesNotLookupEmailOrHashPassword(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisVerification := common.RedisVerificationCodeEnabled
	previousRedisEnabled := common.RedisEnabled
	common.RedisVerificationCodeEnabled = false
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisVerificationCodeEnabled = previousRedisVerification
		common.RedisEnabled = previousRedisEnabled
	})
	gin.SetMode(gin.TestMode)

	user := model.User{Username: "reset-oracle", Password: "password123", Email: "known-reset@example.com", Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	queries := 0
	callbackName := "test:count-reset-confirm-lookups"
	if err := model.CoreDB().Callback().Query().Before("gorm:query").Register(callbackName, func(_ *gorm.DB) {
		queries++
	}); err != nil {
		t.Fatalf("register query counter: %v", err)
	}
	t.Cleanup(func() { _ = model.CoreDB().Callback().Query().Remove(callbackName) })

	engine := gin.New()
	engine.POST("/user/reset/v2", ResetPasswordV2)
	responses := make([]string, 0, 2)
	for _, email := range []string{user.Email, "missing-reset@example.com"} {
		body := fmt.Sprintf(`{"email":%q,"user_id":%d,"session_version":%d,"token":"definitely-invalid-token","password":"replacement-password"}`, email, user.Id, user.SessionVersion)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/user/reset/v2", strings.NewReader(body)))
		responses = append(responses, response.Body.String())
	}
	if responses[0] != responses[1] {
		t.Fatalf("reset confirmation leaks email existence: known=%s missing=%s", responses[0], responses[1])
	}
	if queries != 0 {
		t.Fatalf("invalid reset token triggered %d account lookups", queries)
	}

	// An oversized password makes bcrypt return a distinct error. Seeing the
	// generic token error proves invalid tokens are rejected before hashing.
	_, err := model.ResetUserPasswordWithToken(user.Email, user.Id, user.SessionVersion, "another-invalid-token", strings.Repeat("x", 73))
	if !errors.Is(err, model.ErrPasswordResetLinkInvalid) {
		t.Fatalf("invalid token reached password hashing: %v", err)
	}
}

func TestResetTokenClaimsAreCheckedBeforeConsumption(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisVerification := common.RedisVerificationCodeEnabled
	previousRedisEnabled := common.RedisEnabled
	common.RedisVerificationCodeEnabled = false
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.RedisVerificationCodeEnabled = previousRedisVerification
		common.RedisEnabled = previousRedisEnabled
	})

	user := model.User{Username: "reset-claims", Password: "password123", Email: "claims@example.com", Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "claims-bound-token"
	common.RegisterVerificationCodeWithKey(model.PasswordResetVerificationKey(user.Id, user.SessionVersion), token, common.PasswordResetPurpose)
	if _, err := model.ResetUserPasswordWithToken(user.Email, user.Id+1, user.SessionVersion, token, "replacement-password"); err == nil {
		t.Fatal("wrong owner claim unexpectedly reset a password")
	}
	if _, err := model.ResetUserPasswordWithToken(user.Email, user.Id, user.SessionVersion+1, token, "replacement-password"); err == nil {
		t.Fatal("wrong version claim unexpectedly reset a password")
	}
	if _, err := model.ResetUserPasswordWithToken(user.Email, user.Id, user.SessionVersion, token, "replacement-password"); err != nil {
		t.Fatalf("wrong claims consumed the correctly keyed token: %v", err)
	}
}

func TestEnumerationLookupsRunOutsideRequestPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		handler gin.HandlerFunc
	}{
		{name: "registration", path: "/verification?email=known@example.com", handler: SendEmailVerification},
		{name: "password reset", path: "/reset_password?email=known@example.com", handler: SendPasswordResetEmail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupOIDCTestDB(t)
			user := model.User{Username: "async-known", Password: "password123", Email: "known@example.com", Status: common.UserStatusEnabled}
			if err := user.Insert(0); err != nil {
				t.Fatalf("seed user: %v", err)
			}

			lookupStarted := make(chan struct{}, 1)
			releaseLookup := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseLookup) }) }
			callbackName := "test:block-enumeration-lookup"
			if err := model.CoreDB().Callback().Query().Before("gorm:query").Register(callbackName, func(_ *gorm.DB) {
				select {
				case lookupStarted <- struct{}{}:
				default:
				}
				<-releaseLookup
			}); err != nil {
				t.Fatalf("register lookup callback: %v", err)
			}
			t.Cleanup(func() {
				release()
				waitForAuthEmailDeliveries()
				_ = model.CoreDB().Callback().Query().Remove(callbackName)
			})

			engine := gin.New()
			engine.GET(strings.Split(test.path, "?")[0], test.handler)
			response := httptest.NewRecorder()
			requestDone := make(chan struct{})
			go func() {
				engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
				close(requestDone)
			}()

			select {
			case <-requestDone:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("handler waited for the account lookup")
			}
			if !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("expected fixed success response, got %s", response.Body.String())
			}
			select {
			case <-lookupStarted:
			case <-time.After(time.Second):
				t.Fatal("background account lookup did not start")
			}
			release()
			waitForAuthEmailDeliveries()
		})
	}
}

func TestOnboardingStateCompletesOnlyWhenApplicableStepsAreDone(t *testing.T) {
	base := onboardingState{HasKey: true, HasRequest: true, HasAlert: true}
	if !base.complete() {
		t.Fatal("a password account with all three steps done should be complete")
	}
	social := base
	social.NeedsPassword = true
	social.PasswordSet = false
	if social.complete() {
		t.Fatal("a social account still owing a fallback password is not complete")
	}
	social.PasswordSet = true
	if !social.complete() {
		t.Fatal("a social account with a password set should be complete")
	}
}
