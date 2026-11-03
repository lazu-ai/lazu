package controller

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// A deleted account keeps its row, its address and its identity rows. Every
// door it can still reach answers account_deleted; none of them reports a
// temporary failure, a conflict with somebody else, or a wrong password.

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

func TestSocialOAuthIdentityOfADeletedAccountAnswersDeleted(t *testing.T) {
	setupOIDCTestDB(t)
	identity := socialIdentity{Provider: googleOAuthProvider, Subject: "google-subject-deleted", Username: "returning", Email: "returning@example.com"}
	user, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if err := model.CoreDB().Delete(user).Error; err != nil {
		t.Fatalf("soft delete: %v", err)
	}
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

func TestOIDCIdentityAndEmailOfADeletedAccountAnswerDeleted(t *testing.T) {
	setupOIDCTestDB(t)
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
}

func TestPasswordSignInNamesADeletedAccountOnlyForItsPassword(t *testing.T) {
	setupOIDCTestDB(t)
	seedDeletedAccount(t, "pw-deleted", "pw-deleted@example.com", "correct-password")
	for _, login := range []string{"pw-deleted", "pw-deleted@example.com"} {
		user := model.User{Username: login, Password: "correct-password"}
		if err := user.ValidateAndFill(); !errors.Is(err, model.ErrAccountDeleted) {
			t.Fatalf("%s with its password answered %v", login, err)
		}
		wrong := model.User{Username: login, Password: "wrong-password"}
		if err := wrong.ValidateAndFill(); !types.IsErrCode(err, types.ErrorCodeInvalidCredentials) {
			t.Fatalf("%s with a wrong password revealed more than invalid_credentials: %v", login, err)
		}
	}
	unknown := model.User{Username: "nobody", Password: "correct-password"}
	if err := unknown.ValidateAndFill(); !types.IsErrCode(err, types.ErrorCodeInvalidCredentials) {
		t.Fatalf("unknown name answered %v", err)
	}
}

func TestCodeAndResetMailsTellADeletedAddressItWasDeleted(t *testing.T) {
	setupOIDCTestDB(t)
	seedDeletedAccount(t, "mail-deleted", "mail-deleted@example.com", "password123")
	previousDeliver, previousEmailCode, previousRegister := deliverAuthEmail, common.EmailCodeAuthEnabled, common.RegisterEnabled
	common.EmailCodeAuthEnabled, common.RegisterEnabled = true, true
	var subjects []string
	deliverAuthEmail = func(subject string, _ string, _ string) error {
		subjects = append(subjects, subject)
		return nil
	}
	t.Cleanup(func() {
		deliverAuthEmail, common.EmailCodeAuthEnabled, common.RegisterEnabled = previousDeliver, previousEmailCode, previousRegister
	})

	dispatchPasswordResetEmail("Mail-Deleted@example.com", common.MailZH)
	dispatchEmailAuthCode("mail-deleted@example.com", common.MailZH, false)
	dispatchEmailVerification("mail-deleted@example.com", common.MailZH)

	want := fmt.Sprintf("这个邮箱的 %s 账号已注销", common.SystemName)
	if len(subjects) != 3 {
		t.Fatalf("expected one mail per door, got %q", subjects)
	}
	for i, subject := range subjects {
		if subject != want {
			t.Fatalf("door %d sent %q", i, subject)
		}
	}
}
