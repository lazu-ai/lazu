package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func issueTestCode(t *testing.T, key, code, purpose string) {
	t.Helper()
	if err := common.IssueVerificationCodes(key, common.VerificationCode{Purpose: purpose, Code: code}); err != nil {
		t.Fatal(err)
	}
}

func checkTestCode(t *testing.T, key, code, purpose string, want common.VerificationResult) {
	t.Helper()
	got, err := common.ConsumeVerificationCodes(key, common.VerificationCode{Purpose: purpose, Code: code})
	if err != nil || got != want {
		t.Fatalf("verification result %v, want %v; error %v", got, want, err)
	}
}

func issueEmailAuthCode(t *testing.T, email string) string {
	t.Helper()
	code, err := common.GenerateNumericVerificationCode(6)
	if err != nil {
		t.Fatal(err)
	}
	issueTestCode(t, email, code, common.EmailAuthPurpose)
	return code
}

func enableEmailCodeAuthForTest(t *testing.T) {
	t.Helper()
	previous := common.EmailCodeAuthEnabled
	common.EmailCodeAuthEnabled = true
	t.Cleanup(func() { common.EmailCodeAuthEnabled = previous })
}

func TestEmailVerificationRedisPersistsAcrossAPIProcesses(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not configured")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	setupOIDCTestDB(t)
	enableEmailCodeAuthForTest(t)
	client := redis.NewClient(options)
	previousRedis, previousMode, previousClient := common.RedisEnabled, common.RedisVerificationCodeEnabled, common.RDB
	previousSecret, previousSMTP := common.CryptoSecret, common.SMTPServer
	previousUser, previousToken, previousDelivery := common.SMTPAccount, common.SMTPToken, deliverAuthEmail
	t.Cleanup(func() {
		common.RedisEnabled, common.RedisVerificationCodeEnabled, common.RDB = previousRedis, previousMode, previousClient
		common.CryptoSecret, common.SMTPServer = previousSecret, previousSMTP
		common.SMTPAccount, common.SMTPToken, deliverAuthEmail = previousUser, previousToken, previousDelivery
		_ = client.Close()
	})
	common.RedisEnabled, common.RedisVerificationCodeEnabled, common.RDB = true, true, client
	common.CryptoSecret = "shared-verification-test-key" // secret-scan: allow -- disposable test fixture
	common.SMTPServer, common.SMTPAccount, common.SMTPToken = "smtp.example.test", "test", "test"

	engine := gin.New()
	engine.Use(sessions.Sessions("test", cookie.NewStore([]byte("email-test-cookie"))))
	engine.POST("/start", StartEmailAuth)
	engine.POST("/verify", VerifyEmailAuth)
	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(rec, req)
		return rec
	}
	email := os.Getenv("LAZU_TEST_OTP_ADDRESS")
	if email == "" {
		email = strings.ToLower(common.GenerateULID()) + "@example.test"
	}
	user := bindingUser(t, "mail-recipient", email)
	if inbox := os.Getenv("LAZU_TEST_OTP_INBOX"); inbox != "" {
		deliverAuthEmail = func(_, receiver, html string) error {
			if receiver != user.Email {
				return fmt.Errorf("unexpected test recipient")
			}
			return os.WriteFile(inbox, []byte(html), 0600)
		}
		rec := post("/start", fmt.Sprintf(`{"email":%q}`, email))
		waitForAuthEmailDeliveries()
		if rec.Code != http.StatusOK {
			t.Fatalf("request code: %s", rec.Body.String())
		}
		return
	}

	inbox := filepath.Join(t.TempDir(), "mail.html")
	child := exec.Command(os.Args[0], "-test.run=^TestEmailVerificationRedisPersistsAcrossAPIProcesses$")
	child.Env = append(os.Environ(), "LAZU_TEST_OTP_ADDRESS="+email, "LAZU_TEST_OTP_INBOX="+inbox)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("issuer: %v %s", err, out)
	}
	html, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`>([0-9]{6})</td>`).FindSubmatch(html)
	if len(match) != 2 {
		t.Fatal("test mailbox did not receive a sign-in code")
	}
	body := fmt.Sprintf(`{"email":%q,"code":%q}`, strings.ToUpper(email), string(match[1]))
	common.RDB = nil
	requireUserAPIError(t, post("/verify", body), http.StatusServiceUnavailable, types.ErrorCodeVerificationUnavailable)
	mail := captureAuthMail(t)
	dispatchEmailAuthCode(email, common.MailEN, false)
	sendEmailBindingCodes(user, "target@example.test", common.MailEN, true)
	dispatchPasswordResetEmail(email, common.MailEN)
	if len(*mail) != 0 {
		t.Fatal("a credential email was sent while the verification store was unavailable")
	}
	common.RDB = client
	rec := post("/verify", body)
	if rec.Code != http.StatusOK || len(rec.Result().Cookies()) == 0 {
		t.Fatalf("sign-in after issuer exit: %d %s", rec.Code, rec.Body.String())
	}
	requireUserAPIError(t, post("/verify", body), http.StatusBadRequest, types.ErrorCodeVerificationCodeInvalid)
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

	const wrong = "invalid"
	for attempt := 1; attempt < common.MaxVerificationAttempts; attempt++ {
		checkTestCode(t, email, wrong, common.EmailAuthPurpose, common.VerificationInvalid)
	}
	checkTestCode(t, email, wrong, common.EmailAuthPurpose, common.VerificationExhausted)
	checkTestCode(t, email, code, common.EmailAuthPurpose, common.VerificationInvalid)
}

// A code minted to prove a new address must not also open a session, and a
// sign-in code must not stand in for address verification.
func TestEmailAuthCodesDoNotCrossPurposes(t *testing.T) {
	setupOIDCTestDB(t)
	email := "purpose@example.com"
	code := common.GenerateVerificationCode(6)
	issueTestCode(t, email, code, emailBindingPurpose)
	checkTestCode(t, email, code, common.EmailAuthPurpose, common.VerificationInvalid)
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

func TestCodeMailShowsCredentialOnlyInBody(t *testing.T) {
	const code = "123456"
	for _, lang := range []string{"en", "zh"} {
		check := func(subject, body string) {
			t.Helper()
			if strings.Contains(subject, code) || !strings.Contains(body, code) {
				t.Fatalf("unexpected credential placement for %s", lang)
			}
		}
		check(authLoginCodeMail(lang, code))
		check(authEmailBindingMail(lang, code, "new@example.test"))
	}
}
