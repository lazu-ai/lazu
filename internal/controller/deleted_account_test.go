package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// A deleted account keeps its row, its address and its identity rows. Inside
// the restore window, signing in through any door that proves ownership
// brings it back. Past the window, every door it can still reach answers
// account_deleted; none of them reports a temporary failure, a conflict with
// somebody else, or a wrong password.

func setupDeletedAccountTestDB(t *testing.T) {
	t.Helper()
	setupOIDCTestDB(t)
	if err := model.CoreDB().AutoMigrate(&model.Token{}, &model.Project{}, &model.ProjectMember{}, &model.AccountDeletion{}, &model.WalletLedger{}, &model.TokenCredential{}); err != nil {
		t.Fatalf("migrate tokens and projects: %v", err)
	}
	model.EnsureColumnNamesInitialized()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
}

func seedDeletedAccount(t *testing.T, username, email, password string) model.User {
	t.Helper()
	user := model.User{Username: username, Password: password, Email: email, VerifiedEmail: email, Status: common.UserStatusEnabled}
	if err := user.Insert(0); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := model.CoreDB().Delete(&user).Error; err != nil {
		t.Fatalf("soft delete user: %v", err)
	}
	return user
}

// expireDeletion moves a deletion to just past its restore window.
func expireDeletion(t *testing.T, userID int) {
	t.Helper()
	past := time.Now().Add(-model.AccountRestoreWindow - time.Hour)
	if err := model.CoreDB().Unscoped().Model(&model.User{}).Where("id = ?", userID).Update("deleted_at", past).Error; err != nil {
		t.Fatalf("backdate deletion: %v", err)
	}
}

func captureAuthMail(t *testing.T) *[]string {
	t.Helper()
	previous := deliverAuthEmail
	var subjects []string
	deliverAuthEmail = func(subject string, _ string, _ string) error {
		subjects = append(subjects, subject)
		return nil
	}
	t.Cleanup(func() { deliverAuthEmail = previous })
	return &subjects
}

func TestSocialOAuthIdentityOfAnExpiredDeletionAnswersDeleted(t *testing.T) {
	setupDeletedAccountTestDB(t)
	identity := socialIdentity{Provider: googleOAuthProvider, Subject: "google-subject-deleted", Username: "returning", Email: "returning@example.com"}
	user, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if err := model.CoreDB().Delete(user).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	expireDeletion(t, user.Id)
	var before int64
	model.CoreDB().Unscoped().Model(&model.User{}).Count(&before)

	_, err = resolveSocialOAuthUser(identity, "")
	if !errors.Is(err, model.ErrAccountDeleted) {
		t.Fatalf("returning identity of a deleted account answered %v", err)
	}
	if code := oauthRedirectCode(err); code != types.ErrorCodeAccountDeleted {
		t.Fatalf("redirect collapsed account_deleted into %q", code)
	}
	var after int64
	model.CoreDB().Unscoped().Model(&model.User{}).Count(&after)
	if after != before {
		t.Fatalf("a deleted account's identity created a new user: %d -> %d", before, after)
	}
}

func TestSocialOAuthInsideTheWindowRestoresTheAccount(t *testing.T) {
	setupDeletedAccountTestDB(t)
	mail := captureAuthMail(t)
	identity := socialIdentity{Provider: googleOAuthProvider, Subject: "google-subject-back", Username: "comeback", Email: "comeback@example.com"}
	user, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	waitForAuthEmailDeliveries()
	*mail = nil
	var project model.Project
	if err := model.CoreDB().Where("owner_user_id = ?", user.Id).First(&project).Error; err != nil {
		project = model.Project{OwnerUserID: user.Id, Name: user.Username}
		if err := model.CoreDB().Create(&project).Error; err != nil {
			t.Fatalf("seed project: %v", err)
		}
	}
	other := model.Project{OwnerUserID: user.Id + 1000, Name: "someone-else"}
	if err := model.CoreDB().Create(&other).Error; err != nil {
		t.Fatalf("seed other project: %v", err)
	}
	for _, projectID := range []int{project.ID, other.ID} {
		member := model.ProjectMember{ProjectID: projectID, UserID: user.Id, Role: "owner", Status: model.ProjectMemberActive}
		if err := model.CoreDB().Where("project_id = ? AND user_id = ?", projectID, user.Id).FirstOrCreate(&member).Error; err != nil {
			t.Fatalf("seed membership: %v", err)
		}
	}
	key := model.Token{UserId: user.Id, Name: "agent", Key: "restore-test-key", Status: common.TokenStatusEnabled}
	if err := model.CoreDB().Create(&key).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if _, err := model.DeleteSelfUser(user.Id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var deleted model.User
	model.CoreDB().Unscoped().First(&deleted, user.Id)

	back, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("sign-in inside the window answered %v", err)
	}
	if back.Id != user.Id {
		t.Fatalf("sign-in reached account %d, want the restored %d", back.Id, user.Id)
	}
	var live model.User
	if err := model.CoreDB().First(&live, user.Id).Error; err != nil {
		t.Fatalf("restored account is not live: %v", err)
	}
	if live.SessionVersion <= deleted.SessionVersion {
		t.Fatalf("session version %d did not advance past %d", live.SessionVersion, deleted.SessionVersion)
	}
	var token model.Token
	model.CoreDB().First(&token, key.Id)
	if token.Status != common.TokenStatusDisabled {
		t.Fatalf("restored key status %d, want disabled", token.Status)
	}
	var own, theirs model.ProjectMember
	model.CoreDB().Where("project_id = ? AND user_id = ?", project.ID, user.Id).First(&own)
	model.CoreDB().Where("project_id = ? AND user_id = ?", other.ID, user.Id).First(&theirs)
	if own.Status != model.ProjectMemberActive || own.RemovedAt != nil {
		t.Fatalf("own project membership not restored: %+v", own)
	}
	if theirs.Status != model.ProjectMemberRemoved {
		t.Fatalf("membership in another owner's project came back: %+v", theirs)
	}
	waitForAuthEmailDeliveries()
	// The identity carries no language here, so the notice is in English.
	want, _ := authAccountRestoredMail(common.MailEN, user.Username, time.Now())
	found := false
	for _, subject := range *mail {
		found = found || subject == want
	}
	if !found {
		t.Fatalf("restore sent no notice, mails %q", *mail)
	}
}

func TestOIDCIdentityAndEmailOfADeletedAccountAnswerDeleted(t *testing.T) {
	setupDeletedAccountTestDB(t)
	deleted := seedDeletedAccount(t, "oidc-deleted", "oidc-deleted@example.com", "password123")
	issuer := canonicalOIDCIssuer(common.OIDCIssuerURL)
	if err := model.CoreDB().Create(&model.UserIdentity{UserID: deleted.Id, Provider: oidcIdentityProvider, Subject: oidcNamespacedSubject(issuer, "oidc-sub")}).Error; err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	if _, err := findExistingOIDCUserTx(model.CoreDB(), issuer, "oidc-sub", ""); !errors.Is(err, model.ErrAccountDeleted) {
		t.Fatalf("identity of a deleted account answered %v", err)
	}
	if _, err := findExistingOIDCUserTx(model.CoreDB(), issuer, "other-sub", "OIDC-Deleted@example.com"); !errors.Is(err, model.ErrAccountDeleted) {
		t.Fatalf("email of a deleted account answered %v", err)
	}
	// The door restores ahead of this resolution inside the window, and does
	// nothing once the window has passed.
	if err := restoreDeletedOwnerBeforeSignIn(oidcIdentityProvider, oidcNamespacedSubject(issuer, "oidc-sub"), "", common.MailZH); err != nil {
		t.Fatalf("restore inside the window: %v", err)
	}
	if _, err := findExistingOIDCUserTx(model.CoreDB(), issuer, "oidc-sub", ""); err != nil {
		t.Fatalf("restored account still answers %v", err)
	}
}

func TestPasswordSignInNamesADeletedAccountOnlyForItsPassword(t *testing.T) {
	setupDeletedAccountTestDB(t)
	deleted := seedDeletedAccount(t, "pw-deleted", "pw-deleted@example.com", "correct-password")
	for _, login := range []string{"pw-deleted", "pw-deleted@example.com"} {
		user := model.User{Username: login, Password: "correct-password"}
		if err := user.ValidateAndFill(); !errors.Is(err, model.ErrAccountDeleted) || user.Id != deleted.Id {
			t.Fatalf("%s with its password answered %v for account %d", login, err, user.Id)
		}
		wrong := model.User{Username: login, Password: "wrong-password"}
		if err := wrong.ValidateAndFill(); !types.IsErrCode(err, types.ErrorCodeInvalidCredentials) || wrong.Id != 0 {
			t.Fatalf("%s with a wrong password revealed more than invalid_credentials: %v", login, err)
		}
	}
	unknown := model.User{Username: "nobody", Password: "correct-password"}
	if err := unknown.ValidateAndFill(); !types.IsErrCode(err, types.ErrorCodeInvalidCredentials) {
		t.Fatalf("unknown name answered %v", err)
	}
}

func TestPasswordLoginPastTheWindowAnswersDeleted(t *testing.T) {
	setupDeletedAccountTestDB(t)
	deleted := seedDeletedAccount(t, "pw-expired", "pw-expired@example.com", "correct-password")
	expireDeletion(t, deleted.Id)
	c, w := newUserTestContext(http.MethodPost, "/api/user/login", `{"username":"pw-expired","password":"correct-password"}`)
	Login(c)
	requireUserAPIError(t, w, http.StatusForbidden, types.ErrorCodeAccountDeleted)
	if err := model.CoreDB().First(&model.User{}, deleted.Id).Error; err == nil {
		t.Fatal("an expired deletion was restored")
	}
}

func TestPasswordDoorRestoresInsideTheWindow(t *testing.T) {
	setupDeletedAccountTestDB(t)
	deleted := seedDeletedAccount(t, "pw-back", "pw-back@example.com", "correct-password")
	user := model.User{Username: "pw-back", Password: "correct-password"}
	if err := user.ValidateAndFill(); !errors.Is(err, model.ErrAccountDeleted) {
		t.Fatalf("validate answered %v", err)
	}
	c, _ := newUserTestContext(http.MethodPost, "/api/user/login", "")
	restored, err := restoreDeletedAccountForSignIn(c, &user)
	if err != nil || restored.Id != deleted.Id || restored.DeletedAt.Valid {
		t.Fatalf("restore answered %+v, %v", restored, err)
	}
	again := model.User{Username: "pw-back", Password: "correct-password"}
	if err := again.ValidateAndFill(); err != nil || again.Id != deleted.Id {
		t.Fatalf("restored account signs in with %v", err)
	}
}

func TestCodeAndResetMailsTellADeletedAddressItWasDeleted(t *testing.T) {
	setupDeletedAccountTestDB(t)
	deleted := seedDeletedAccount(t, "mail-deleted", "mail-deleted@example.com", "password123")
	previousEmailCode, previousRegister := common.EmailCodeAuthEnabled, common.RegisterEnabled
	common.EmailCodeAuthEnabled, common.RegisterEnabled = true, true
	t.Cleanup(func() {
		common.EmailCodeAuthEnabled, common.RegisterEnabled = previousEmailCode, previousRegister
	})
	mail := captureAuthMail(t)
	want := fmt.Sprintf("这个邮箱的 %s 账号已注销", common.SystemName)

	// Inside the window the code door sends a code, since signing in with it
	// restores the account; sign-up and reset still say it was deleted.
	dispatchPasswordResetEmail("Mail-Deleted@example.com", common.MailZH)
	dispatchEmailAuthCode("mail-deleted@example.com", common.MailZH, false)
	dispatchEmailVerification("mail-deleted@example.com", common.MailZH)
	if len(*mail) != 3 || (*mail)[0] != want || (*mail)[2] != want || strings.Contains((*mail)[1], "注销") {
		t.Fatalf("inside the window got %q", *mail)
	}

	expireDeletion(t, deleted.Id)
	*mail = nil
	dispatchPasswordResetEmail("Mail-Deleted@example.com", common.MailZH)
	dispatchEmailAuthCode("mail-deleted@example.com", common.MailZH, false)
	dispatchEmailVerification("mail-deleted@example.com", common.MailZH)
	if len(*mail) != 3 {
		t.Fatalf("expected one mail per door, got %q", *mail)
	}
	for i, subject := range *mail {
		if subject != want {
			t.Fatalf("door %d sent %q", i, subject)
		}
	}
}

func TestEmailCodeSignInRestoresInsideTheWindow(t *testing.T) {
	setupDeletedAccountTestDB(t)
	deleted := seedDeletedAccount(t, "code-back", "code-back@example.com", "password123")
	previous := common.EmailCodeAuthEnabled
	common.EmailCodeAuthEnabled = true
	t.Cleanup(func() { common.EmailCodeAuthEnabled = previous })
	if err := restoreDeletedOwnerBeforeSignIn("", "", "Code-Back@example.com", common.MailZH); err != nil {
		t.Fatalf("restore: %v", err)
	}
	user, created, err := resolveEmailAuthUser("code-back@example.com", "")
	if err != nil || created || user.Id != deleted.Id {
		t.Fatalf("code sign-in reached %+v created=%v err=%v", user, created, err)
	}
}

func TestAdminRestoresAndRemovesADeletedAccount(t *testing.T) {
	setupDeletedAccountTestDB(t)
	seedStandingAdmin(t)
	deleted := seedDeletedAccount(t, "admin-target", "admin-target@example.com", "password123")
	expireDeletion(t, deleted.Id)

	c, w := newUserTestContext(http.MethodPost, "/api/user/manage", fmt.Sprintf(`{"id":%q,"action":"disable"}`, deleted.PublicID))
	c.Set("role", common.RoleRootUser)
	ManageUser(c)
	requireUserAPIError(t, w, http.StatusForbidden, types.ErrorCodeAccountDeleted)

	// An administrator is not held to the window.
	c, w = newUserTestContext(http.MethodPost, "/api/user/manage", fmt.Sprintf(`{"id":%q,"action":"restore"}`, deleted.PublicID))
	c.Set("role", common.RoleRootUser)
	ManageUser(c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("admin restore answered %d %s", w.Code, w.Body.String())
	}
	if err := model.CoreDB().First(&model.User{}, deleted.Id).Error; err != nil {
		t.Fatalf("admin restore left the account deleted: %v", err)
	}

	// A soft-deleted account can still be removed for good.
	if _, err := model.DeleteSelfUser(deleted.Id); err != nil {
		t.Fatalf("delete again: %v", err)
	}
	c, w = newUserTestContext(http.MethodDelete, "/api/user/"+deleted.PublicID, "")
	c.Params = gin.Params{{Key: "id", Value: deleted.PublicID}}
	c.Set("role", common.RoleRootUser)
	DeleteUser(c)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("hard delete of a deleted account answered %d %s", w.Code, w.Body.String())
	}
	var count int64
	model.CoreDB().Unscoped().Model(&model.User{}).Where("id = ?", deleted.Id).Count(&count)
	if count != 0 {
		t.Fatal("hard delete left the row")
	}
}

func TestAClearedAccountsGoogleIdentitySignsUpAgain(t *testing.T) {
	setupDeletedAccountTestDB(t)
	identity := socialIdentity{Provider: googleOAuthProvider, Subject: "google-subject-cleared", Username: "cleared", Email: "cleared@example.com"}
	first, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if _, err := model.DeleteSelfUser(first.Id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	expireDeletion(t, first.Id)
	if purged, err := model.PurgeExpiredDeletedAccounts(time.Now()); err != nil || purged != 1 {
		t.Fatalf("purge = %d, %v", purged, err)
	}
	second, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("sign-in after the account was cleared answered %v", err)
	}
	if second.Id == first.Id {
		t.Fatal("the cleared account came back instead of a new one")
	}
}
