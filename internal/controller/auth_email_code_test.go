package controller

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

func issueEmailAuthCode(t *testing.T, email string) string {
	t.Helper()
	code := common.GenerateVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.EmailAuthPurpose)
	common.ClearVerificationFailures(email, common.EmailAuthPurpose)
	t.Cleanup(func() {
		common.DeleteKey(email, common.EmailAuthPurpose)
		common.ClearVerificationFailures(email, common.EmailAuthPurpose)
	})
	return code
}

func enableEmailCodeAuthForTest(t *testing.T) {
	t.Helper()
	previous := common.EmailCodeAuthEnabled
	common.EmailCodeAuthEnabled = true
	t.Cleanup(func() { common.EmailCodeAuthEnabled = previous })
}

func TestEmailCodeCreatesAnAccountForAnUnknownAddress(t *testing.T) {
	setupOIDCTestDB(t)
	enableEmailCodeAuthForTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = true
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	before := time.Now().Unix()
	user, created, err := resolveEmailAuthUser("newcomer@example.com", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !created {
		t.Fatal("an unknown address should open an account")
	}
	if user.Username != "newcomer" {
		t.Fatalf("the handle should come from the address, got %q", user.Username)
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !stored.GetSetting().NeedsPassword {
		t.Fatal("an account created without a password must be flagged as needing one")
	}
	if stored.TermsVersion != common.TermsVersion || stored.TermsAgreedAt < before {
		t.Fatalf("consent should be recorded, got version %q at %d", stored.TermsVersion, stored.TermsAgreedAt)
	}
}

// The same door signs an existing account in. It must attach to that account
// rather than opening a second one for the same mailbox.
func TestEmailCodeSignsInAnExistingAddress(t *testing.T) {
	setupOIDCTestDB(t)
	enableEmailCodeAuthForTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = true
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	first, created, err := resolveEmailAuthUser("repeat@example.com", "")
	if err != nil || !created {
		t.Fatalf("first resolve: created=%v err=%v", created, err)
	}
	second, created, err := resolveEmailAuthUser("repeat@example.com", "")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if created {
		t.Fatal("a known address must sign in, not sign up again")
	}
	if second.Id != first.Id {
		t.Fatalf("expected the same account, got %d then %d", first.Id, second.Id)
	}

	var count int64
	if err := model.CoreDB().Model(&model.User{}).Where("LOWER(email) = ?", "repeat@example.com").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("one mailbox must map to one account, got %d", count)
	}
}

// An email-code sign-up and a provider callback for the same mailbox take the
// same verified-email lock, so they converge on one account instead of racing
// into two.
func TestEmailCodeAndProviderSignInConvergeOnOneAccount(t *testing.T) {
	setupOIDCTestDB(t)
	enableEmailCodeAuthForTest(t)
	defer waitForAuthEmailDeliveries()
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = true
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	emailUser, created, err := resolveEmailAuthUser("shared@example.com", "")
	if err != nil || !created {
		t.Fatalf("email resolve: created=%v err=%v", created, err)
	}
	providerUser, err := resolveSocialOAuthUser(socialIdentity{
		Provider: googleOAuthProvider,
		Subject:  "shared-subject",
		Username: "shared",
		Email:    "Shared@Example.com",
	}, "")
	if err != nil {
		t.Fatalf("provider resolve: %v", err)
	}
	if providerUser.Id != emailUser.Id {
		t.Fatalf("both doors should reach one account, got %d and %d", emailUser.Id, providerUser.Id)
	}
}

func TestEmailCodeHonoursClosedRegistration(t *testing.T) {
	setupOIDCTestDB(t)
	enableEmailCodeAuthForTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = false
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	if _, _, err := resolveEmailAuthUser("locked-out@example.com", ""); err == nil {
		t.Fatal("closed registration must refuse an unknown address")
	}
	var count int64
	if err := model.CoreDB().Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("closed registration created %d account(s)", count)
	}
}

// A handle already taken by someone else must not block the sign-up; the
// derived username gets a suffix instead.
func TestEmailCodeSuffixesAContestedHandle(t *testing.T) {
	setupOIDCTestDB(t)
	enableEmailCodeAuthForTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = true
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	first, _, err := resolveEmailAuthUser("sam@one.example.com", "")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, _, err := resolveEmailAuthUser("sam@two.example.com", "")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Username != "sam" {
		t.Fatalf("first account should take the plain handle, got %q", first.Username)
	}
	if second.Username == first.Username {
		t.Fatalf("the second account must not reuse %q", first.Username)
	}
	if !strings.HasPrefix(second.Username, "sam") {
		t.Fatalf("the suffixed handle should still derive from the address, got %q", second.Username)
	}
}

func TestEmailCodeAuthMustBeExplicitlyEnabled(t *testing.T) {
	setupOIDCTestDB(t)
	previous := common.EmailCodeAuthEnabled
	common.EmailCodeAuthEnabled = false
	t.Cleanup(func() { common.EmailCodeAuthEnabled = previous })

	if _, _, err := resolveEmailAuthUser("sso-only@example.com", ""); !errors.Is(err, errEmailAuthDisabled) {
		t.Fatalf("email auth should be unavailable without its own policy flag, got %v", err)
	}
}

// Six digits is a small space. Wrong guesses burn the code so an attacker
// cannot keep narrowing it within one code's lifetime.
func TestWrongCodesBurnTheCode(t *testing.T) {
	setupOIDCTestDB(t)
	email := "brute@example.com"
	code := issueEmailAuthCode(t, email)

	for attempt := 1; attempt < common.MaxVerificationAttempts; attempt++ {
		if common.VerifyCodeWithKey(email, "000000", common.EmailAuthPurpose) {
			t.Fatal("a wrong code must never verify")
		}
		if burned := common.RecordVerificationFailure(email, common.EmailAuthPurpose); burned {
			t.Fatalf("the code was burned early, on attempt %d", attempt)
		}
	}
	if common.VerifyCodeWithKey(email, "000000", common.EmailAuthPurpose) {
		t.Fatal("a wrong code must never verify")
	}
	if burned := common.RecordVerificationFailure(email, common.EmailAuthPurpose); !burned {
		t.Fatalf("the code should be burned after %d wrong guesses", common.MaxVerificationAttempts)
	}
	if common.VerifyCodeWithKey(email, code, common.EmailAuthPurpose) {
		t.Fatal("the real code must stop working once it is burned")
	}
}

// A code minted to prove a new address must not also open a session, and a
// sign-in code must not stand in for address verification.
func TestEmailAuthCodesDoNotCrossPurposes(t *testing.T) {
	setupOIDCTestDB(t)
	email := "purpose@example.com"
	code := common.GenerateVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.EmailVerificationPurpose)
	t.Cleanup(func() { common.DeleteKey(email, common.EmailVerificationPurpose) })

	if common.VerifyCodeWithKey(email, code, common.EmailAuthPurpose) {
		t.Fatal("an address-verification code must not sign anyone in")
	}
}

func TestValidateEmailForAuthAppliesDeploymentPolicy(t *testing.T) {
	previousDomainRestriction := common.EmailDomainRestrictionEnabled
	previousWhitelist := common.EmailDomainWhitelist
	previousAliasRestriction := common.EmailAliasRestrictionEnabled
	t.Cleanup(func() {
		common.EmailDomainRestrictionEnabled = previousDomainRestriction
		common.EmailDomainWhitelist = previousWhitelist
		common.EmailAliasRestrictionEnabled = previousAliasRestriction
	})

	common.EmailDomainRestrictionEnabled = true
	common.EmailDomainWhitelist = []string{"allowed.example.com"}
	common.EmailAliasRestrictionEnabled = true

	if err := validateEmailForAuth("person@allowed.example.com"); err != nil {
		t.Fatalf("a whitelisted address should pass, got %v", err)
	}
	if err := validateEmailForAuth("person@blocked.example.com"); err != errEmailAuthPolicyDomain {
		t.Fatalf("an off-whitelist domain should be refused, got %v", err)
	}
	if err := validateEmailForAuth("person+tag@allowed.example.com"); err != errEmailAuthPolicyAlias {
		t.Fatalf("an alias should be refused when aliases are restricted, got %v", err)
	}
	if err := validateEmailForAuth("not-an-address"); err == nil {
		t.Fatal("a malformed address should be refused")
	}
}

func TestEmailCodeLetsAnInvitedAddressInWhileRegistrationIsClosed(t *testing.T) {
	setupOIDCTestDB(t)
	enableEmailCodeAuthForTest(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = false
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	user, created, err := resolveEmailAuthUserFor("invited@example.com", true)
	if err != nil || !created {
		t.Fatalf("an invited address should open an account: created=%v err=%v", created, err)
	}
	if !user.HasVerifiedEmail() {
		t.Fatal("the code proved the address, so it must count as verified")
	}
}
