package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

func TestConfiguredSocialAuthProvidersRequireCompleteCredentials(t *testing.T) {
	previousGoogleID := common.GoogleOAuthClientID
	previousGoogleSecret := common.GoogleOAuthClientSecret
	previousGitHubID := common.GitHubOAuthClientID
	previousGitHubSecret := common.GitHubOAuthClientSecret
	t.Cleanup(func() {
		common.GoogleOAuthClientID = previousGoogleID
		common.GoogleOAuthClientSecret = previousGoogleSecret
		common.GitHubOAuthClientID = previousGitHubID
		common.GitHubOAuthClientSecret = previousGitHubSecret
	})

	common.GoogleOAuthClientID = "google-id"
	common.GoogleOAuthClientSecret = ""
	common.GitHubOAuthClientID = "github-id"
	common.GitHubOAuthClientSecret = "placeholder-github-secret"

	providers := configuredSocialAuthProviders()
	if len(providers) != 1 || providers[0].ID != githubOAuthProvider {
		t.Fatalf("unexpected configured providers: %+v", providers)
	}
}

// Signing in with a provider we have never seen creates the account. The old
// behaviour — refusing unless the callback carried intent=register — turned
// "sign in with Google" into a dead end for every new person, which is the bug
// this replaced. Consent is now the notice under the button plus the version
// and timestamp stamped on the row.
func TestSocialLoginCreatesTheAccountAndRecordsConsent(t *testing.T) {
	setupOIDCTestDB(t)
	defer waitForAuthEmailDeliveries()
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = true
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	before := time.Now().Unix()
	user, err := resolveSocialOAuthUser(socialIdentity{
		Provider: githubOAuthProvider,
		Subject:  "login-only-subject",
		Username: "login-only",
		Email:    "login-only@example.com",
	}, "")
	if err != nil {
		t.Fatalf("provider sign-in should open an account, got %v", err)
	}
	if user == nil || user.Id == 0 {
		t.Fatal("provider sign-in returned no account")
	}

	var stored model.User
	if err := model.CoreDB().First(&stored, user.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.TermsVersion != common.TermsVersion {
		t.Fatalf("account should record the terms in force, got %q", stored.TermsVersion)
	}
	if stored.TermsAgreedAt < before {
		t.Fatalf("account should record when the terms were accepted, got %d", stored.TermsAgreedAt)
	}
}

// Registration being closed is still enforced; only the intent gate is gone.
func TestSocialLoginStillHonoursClosedRegistration(t *testing.T) {
	setupOIDCTestDB(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = false
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	_, err := resolveSocialOAuthUser(socialIdentity{
		Provider: githubOAuthProvider,
		Subject:  "closed-subject",
		Username: "closed",
		Email:    "closed@example.com",
	}, "")
	if !errors.Is(err, errOAuthRegistrationClosed) {
		t.Fatalf("closed registration should refuse the sign-up, got %v", err)
	}
	var count int64
	if err := model.CoreDB().Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Fatalf("closed registration created %d account(s)", count)
	}
}

func TestConcurrentSocialRegistrationsConvergeOnOneUser(t *testing.T) {
	setupOIDCTestDB(t)
	defer waitForAuthEmailDeliveries()
	sqlDB, err := model.CoreDB().DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	// Reproduce the production failure mode that motivated transaction-scoped
	// advisory locks: the resolver must never hold the only connection and then
	// ask the global GORM pool for a second one.
	sqlDB.SetMaxOpenConns(1)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = true
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	identities := []socialIdentity{
		{Provider: googleOAuthProvider, Subject: "concurrent-google", Username: "same-person", Email: "Same@Example.com"},
		{Provider: githubOAuthProvider, Subject: "concurrent-github", Username: "same-person", Email: "same@example.com"},
	}
	results := make([]*model.User, len(identities))
	errs := make([]error, len(identities))
	var wait sync.WaitGroup
	for index := range identities {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = resolveSocialOAuthUser(identities[index], "")
		}(index)
	}
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent OAuth resolution deadlocked with max_open_conns=1")
	}
	for index, err := range errs {
		if err != nil {
			t.Fatalf("registration %d failed: %v", index, err)
		}
	}
	if results[0].Id != results[1].Id {
		t.Fatalf("concurrent providers created different users: %d and %d", results[0].Id, results[1].Id)
	}
	var userCount, identityCount int64
	if err := model.CoreDB().Model(&model.User{}).Where("LOWER(email) = ?", "same@example.com").Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := model.CoreDB().Model(&model.UserIdentity{}).Where("user_id = ?", results[0].Id).Count(&identityCount).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if userCount != 1 || identityCount != 2 {
		t.Fatalf("expected one user with two identities, got users=%d identities=%d", userCount, identityCount)
	}
}

func TestConcurrentPasswordAndSocialRegistrationShareEmailOwner(t *testing.T) {
	setupOIDCTestDB(t)
	defer waitForAuthEmailDeliveries()
	gin.SetMode(gin.TestMode)

	previousRegisterEnabled := common.RegisterEnabled
	previousPasswordRegisterEnabled := common.PasswordRegisterEnabled
	previousEmailVerificationEnabled := common.EmailVerificationEnabled
	previousRedisVerificationEnabled := common.RedisVerificationCodeEnabled
	previousRedisEnabled := common.RedisEnabled
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = true
	common.RedisVerificationCodeEnabled = false
	common.RedisEnabled = false
	constant.GenerateDefaultToken = false
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegisterEnabled
		common.PasswordRegisterEnabled = previousPasswordRegisterEnabled
		common.EmailVerificationEnabled = previousEmailVerificationEnabled
		common.RedisVerificationCodeEnabled = previousRedisVerificationEnabled
		common.RedisEnabled = previousRedisEnabled
		constant.GenerateDefaultToken = previousGenerateDefaultToken
	})

	const passwordEmail = "Race@Example.com"
	const verificationCode = "123456"
	common.RegisterVerificationCodeWithKey(passwordEmail, verificationCode, common.EmailVerificationPurpose)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("password-social-race-session-secret"))))
	engine.POST("/register", Register)

	start := make(chan struct{})
	var registerBody string
	var socialUser *model.User
	var socialErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(
			`{"username":"password-racer","password":"password123","email":"Race@Example.com","verification_code":"123456"}`,
		))
		request.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, request)
		registerBody = recorder.Body.String()
	}()
	go func() {
		defer wait.Done()
		<-start
		socialUser, socialErr = resolveSocialOAuthUser(socialIdentity{
			Provider: githubOAuthProvider,
			Subject:  "password-social-race-subject",
			Username: "social-racer",
			Email:    "race@example.com",
		}, "")
	}()
	close(start)
	wait.Wait()

	if socialErr != nil {
		t.Fatalf("social registration failed: %v (password response %s)", socialErr, registerBody)
	}
	if socialUser == nil || socialUser.Id <= 0 {
		t.Fatalf("social registration returned no owner: %+v", socialUser)
	}
	var userCount, identityCount int64
	if err := model.CoreDB().Model(&model.User{}).Where("LOWER(email) = ?", "race@example.com").Count(&userCount).Error; err != nil {
		t.Fatalf("count normalized email owners: %v", err)
	}
	if err := model.CoreDB().Model(&model.UserIdentity{}).
		Where("provider = ? AND subject = ?", githubOAuthProvider, "password-social-race-subject").
		Count(&identityCount).Error; err != nil {
		t.Fatalf("count social identity: %v", err)
	}
	if userCount != 1 || identityCount != 1 {
		t.Fatalf("concurrent password/social registration split email ownership: users=%d identities=%d password=%s", userCount, identityCount, registerBody)
	}
}

func TestSocialRegistrationDefaultTokenFailureIsBestEffort(t *testing.T) {
	setupOIDCTestDB(t)
	previousRegisterEnabled := common.RegisterEnabled
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	common.RegisterEnabled = true
	// setupOIDCTestDB deliberately has no tokens table, forcing the post-commit
	// initializer to fail after the user+identity transaction has committed. The
	// callback must still succeed because the account and identity are valid and
	// the user can create an API key manually.
	constant.GenerateDefaultToken = true
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegisterEnabled
		constant.GenerateDefaultToken = previousGenerateDefaultToken
	})

	identity := socialIdentity{
		Provider: githubOAuthProvider,
		Subject:  "post-commit-retry",
		Username: "post-commit-retry",
		Email:    "post-commit-retry@example.com",
	}
	created, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("post-commit default-token failure rejected social registration: %v", err)
	}
	resolved, err := resolveSocialOAuthUser(identity, "")
	if err != nil {
		t.Fatalf("retry existing external identity: %v", err)
	}
	if resolved.Id != created.Id {
		t.Fatalf("retry resolved a different account: created=%d resolved=%d", created.Id, resolved.Id)
	}

	var userCount, identityCount int64
	if err := model.CoreDB().Model(&model.User{}).Where("LOWER(email) = ?", identity.Email).Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := model.CoreDB().Model(&model.UserIdentity{}).
		Where("provider = ? AND subject = ? AND user_id = ?", identity.Provider, identity.Subject, resolved.Id).
		Count(&identityCount).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if userCount != 1 || identityCount != 1 {
		t.Fatalf("best-effort default-token failure corrupted core identity state: users=%d identities=%d", userCount, identityCount)
	}
}

func TestPasswordRegistrationDefaultTokenKeyGenerationFailureIsBestEffort(t *testing.T) {
	setupOIDCTestDB(t)
	gin.SetMode(gin.TestMode)

	previousRegisterEnabled := common.RegisterEnabled
	previousPasswordRegisterEnabled := common.PasswordRegisterEnabled
	previousEmailVerificationEnabled := common.EmailVerificationEnabled
	previousRedisEnabled := common.RedisEnabled
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	previousGenerateDefaultTokenKey := generateDefaultTokenKey
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = false
	common.RedisEnabled = false
	constant.GenerateDefaultToken = true
	generateDefaultTokenKey = func() (string, error) {
		return "", errors.New("entropy source unavailable")
	}
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegisterEnabled
		common.PasswordRegisterEnabled = previousPasswordRegisterEnabled
		common.EmailVerificationEnabled = previousEmailVerificationEnabled
		common.RedisEnabled = previousRedisEnabled
		constant.GenerateDefaultToken = previousGenerateDefaultToken
		generateDefaultTokenKey = previousGenerateDefaultTokenKey
	})

	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("default-token-best-effort-test-secret"))))
	engine.POST("/register", Register)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/register",
		strings.NewReader(`{"username":"keygen-failure","password":"password123"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	if !strings.Contains(recorder.Body.String(), `"success":true`) {
		t.Fatalf("post-commit key generation failure rejected password registration: %s", recorder.Body.String())
	}
	var created model.User
	if err := model.CoreDB().Where("username = ?", "keygen-failure").First(&created).Error; err != nil {
		t.Fatalf("registered user was not committed: %v", err)
	}
}

func TestSocialRegistrationRollsBackUserWhenIdentityConflicts(t *testing.T) {
	setupOIDCTestDB(t)
	existing := model.User{Username: "identity-owner", Password: "password123", Email: "owner@example.com", Status: common.UserStatusEnabled}
	if err := existing.Insert(0); err != nil {
		t.Fatalf("seed identity owner: %v", err)
	}
	if err := model.CoreDB().Create(&model.UserIdentity{
		UserID: existing.Id, Provider: githubOAuthProvider, Subject: "owned-subject", Email: existing.Email,
	}).Error; err != nil {
		t.Fatalf("seed owned identity: %v", err)
	}

	_, err := createSocialOAuthUser(socialIdentity{
		Provider: githubOAuthProvider,
		Subject:  "owned-subject",
		Username: "would-be-orphan",
		Email:    "orphan@example.com",
	}, "")
	if err == nil {
		t.Fatal("expected provider-subject conflict")
	}
	var count int64
	if err := model.CoreDB().Model(&model.User{}).Where("email = ?", "orphan@example.com").Count(&count).Error; err != nil {
		t.Fatalf("count orphan users: %v", err)
	}
	if count != 0 {
		t.Fatalf("identity conflict left %d orphan user(s)", count)
	}
}

func TestSocialOAuthRejectsSoftDeletedEmail(t *testing.T) {
	setupOIDCTestDB(t)
	deleted := model.User{Username: "deleted-social", Password: "password123", Email: "deleted@example.com", Status: common.UserStatusEnabled}
	if err := deleted.Insert(0); err != nil {
		t.Fatalf("seed deleted user: %v", err)
	}
	if err := model.CoreDB().Delete(&deleted).Error; err != nil {
		t.Fatalf("soft delete user: %v", err)
	}
	_, err := resolveSocialOAuthUser(socialIdentity{
		Provider: googleOAuthProvider,
		Subject:  "deleted-email-subject",
		Username: "deleted-social",
		Email:    "Deleted@example.com",
	}, "")
	if !errors.Is(err, errOAuthEmailConflict) {
		t.Fatalf("soft-deleted email should remain reserved, got %v", err)
	}
}

func TestSocialOAuthExistingIdentityDoesNotBackfillConflictingEmail(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		softDelete bool
	}{
		{name: "active owner"},
		{name: "soft-deleted owner", softDelete: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setupOIDCTestDB(t)
			identityOwner := model.User{
				Username: "existing-identity-owner",
				Password: "password123",
				Status:   common.UserStatusEnabled,
			}
			if err := model.CoreDB().Create(&identityOwner).Error; err != nil {
				t.Fatalf("seed identity owner: %v", err)
			}
			identity := model.UserIdentity{
				UserID:   identityOwner.Id,
				Provider: googleOAuthProvider,
				Subject:  "existing-identity-subject",
			}
			if err := model.CoreDB().Create(&identity).Error; err != nil {
				t.Fatalf("seed identity: %v", err)
			}
			emailOwner := model.User{
				Username: "conflicting-email-owner",
				Password: "password123",
				Email:    "reserved@example.com",
				Status:   common.UserStatusEnabled,
			}
			if err := model.CoreDB().Create(&emailOwner).Error; err != nil {
				t.Fatalf("seed email owner: %v", err)
			}
			if testCase.softDelete {
				if err := model.CoreDB().Delete(&emailOwner).Error; err != nil {
					t.Fatalf("soft delete email owner: %v", err)
				}
			}

			_, err := resolveSocialOAuthUser(socialIdentity{
				Provider: googleOAuthProvider,
				Subject:  identity.Subject,
				Username: identityOwner.Username,
				Email:    "Reserved@Example.com",
			}, "")
			if !errors.Is(err, errOAuthEmailConflict) {
				t.Fatalf("expected email conflict, got %v", err)
			}

			var storedUser model.User
			if err := model.CoreDB().First(&storedUser, identityOwner.Id).Error; err != nil {
				t.Fatalf("reload identity owner: %v", err)
			}
			if storedUser.Email != "" {
				t.Fatalf("conflicting email was backfilled: %q", storedUser.Email)
			}
			var storedIdentity model.UserIdentity
			if err := model.CoreDB().First(&storedIdentity, identity.ID).Error; err != nil {
				t.Fatalf("reload identity: %v", err)
			}
			if storedIdentity.Email != "" {
				t.Fatalf("identity email changed despite rollback: %q", storedIdentity.Email)
			}
		})
	}
}

func TestSocialOAuthLoginUsesStateAndPKCE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousID := common.GitHubOAuthClientID
	previousSecret := common.GitHubOAuthClientSecret
	previousRedirect := common.GitHubOAuthRedirectURL
	previousSessionSecret := common.SessionSecret
	common.GitHubOAuthClientID = "github-id"
	common.GitHubOAuthClientSecret = "placeholder-github-secret"
	common.GitHubOAuthRedirectURL = "https://api.example.com/api/auth/oauth/github/callback"
	common.SessionSecret = "placeholder-session-value-with-enough-entropy"
	t.Cleanup(func() {
		common.GitHubOAuthClientID = previousID
		common.GitHubOAuthClientSecret = previousSecret
		common.GitHubOAuthRedirectURL = previousRedirect
		common.SessionSecret = previousSessionSecret
	})

	engine := gin.New()
	engine.GET("/api/auth/oauth/:provider/login", SocialOAuthLogin)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/github/login?next=%2Fconsole", nil)
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusFound {
		t.Fatalf("expected oauth redirect, got %d: %s", recorder.Code, recorder.Body.String())
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse oauth redirect: %v", err)
	}
	query := location.Query()
	if query.Get("state") == "" || query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("missing state or PKCE parameters: %s", location.String())
	}
	if query.Get("redirect_uri") != common.GitHubOAuthRedirectURL {
		t.Fatalf("unexpected redirect_uri: %q", query.Get("redirect_uri"))
	}
	if len(recorder.Result().Cookies()) != 1 || recorder.Result().Cookies()[0].Path != "/api/auth/oauth" {
		t.Fatalf("expected scoped oauth transaction cookie, got %+v", recorder.Result().Cookies())
	}
}

func TestSocialOAuthCallbackRedirectsErrorsToLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousSessionSecret := common.SessionSecret
	common.SessionSecret = "placeholder-session-value-with-enough-entropy"
	t.Cleanup(func() { common.SessionSecret = previousSessionSecret })

	engine := gin.New()
	engine.GET("/api/auth/oauth/:provider/callback", SocialOAuthCallback)

	cases := []struct {
		name    string
		target  string
		wantKey types.ErrorCode
	}{
		{"unknown provider", "/api/auth/oauth/gitlab/callback", types.ErrorCodeOAuthProviderNotConfigured},
		{"provider denial", "/api/auth/oauth/github/callback?error=access_denied", types.ErrorCodeOAuthCancelled},
		{"missing transaction", "/api/auth/oauth/github/callback?state=whatever", types.ErrorCodeOAuthStateInvalid},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if recorder.Code != http.StatusFound {
			t.Fatalf("%s: expected redirect, got %d: %s", tc.name, recorder.Code, recorder.Body.String())
		}
		location, err := url.Parse(recorder.Header().Get("Location"))
		if err != nil {
			t.Fatalf("%s: parse redirect: %v", tc.name, err)
		}
		if location.Path != "/login" || location.Query().Get("error") != string(tc.wantKey) {
			t.Fatalf("%s: unexpected redirect target %q", tc.name, location.String())
		}
	}
}

func TestResolveSocialOAuthUserLinksMultipleProvidersByVerifiedEmail(t *testing.T) {
	setupOIDCTestDB(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = true
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	existing := model.User{
		Username: "existing-social",
		Password: "password123",
		Email:    "person@example.com",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&existing).Error; err != nil {
		t.Fatalf("seed existing user: %v", err)
	}

	googleUser, err := resolveSocialOAuthUser(socialIdentity{
		Provider: googleOAuthProvider,
		Subject:  "google-subject",
		Username: "person",
		Email:    "Person@example.com",
	}, "")
	if err != nil {
		t.Fatalf("resolve google identity: %v", err)
	}
	githubUser, err := resolveSocialOAuthUser(socialIdentity{
		Provider: githubOAuthProvider,
		Subject:  "12345",
		Username: "octoperson",
		Email:    "person@example.com",
	}, "")
	if err != nil {
		t.Fatalf("resolve github identity: %v", err)
	}
	if googleUser.Id != existing.Id || githubUser.Id != existing.Id {
		t.Fatalf("expected both identities to link user %d, got google=%d github=%d", existing.Id, googleUser.Id, githubUser.Id)
	}
	if githubUser.Role != common.RoleRootUser {
		t.Fatalf("social login must preserve existing role, got %d", githubUser.Role)
	}
	var identityCount int64
	if err := model.CoreDB().Model(&model.UserIdentity{}).Where("user_id = ?", existing.Id).Count(&identityCount).Error; err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identityCount != 2 {
		t.Fatalf("expected two linked identities, got %d", identityCount)
	}
}

func TestResolveSocialOAuthUserHonorsRegistrationDisabled(t *testing.T) {
	setupOIDCTestDB(t)
	previousRegisterEnabled := common.RegisterEnabled
	common.RegisterEnabled = false
	t.Cleanup(func() { common.RegisterEnabled = previousRegisterEnabled })

	_, err := resolveSocialOAuthUser(socialIdentity{
		Provider: githubOAuthProvider,
		Subject:  "999",
		Username: "new-user",
		Email:    "new-user@example.com",
	}, "")
	if err == nil {
		t.Fatal("expected registration-disabled error")
	}
	var count int64
	if err := model.CoreDB().Model(&model.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no user creation, got %d", count)
	}
}

func TestSocialOAuthNewUserGetsDefaultToken(t *testing.T) {
	setupOIDCTestDB(t)
	defer waitForAuthEmailDeliveries()
	if err := model.CoreDB().AutoMigrate(&model.Token{}); err != nil {
		t.Fatalf("migrate signup features: %v", err)
	}
	previousRegisterEnabled := common.RegisterEnabled
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	common.RegisterEnabled = true
	constant.GenerateDefaultToken = true
	t.Cleanup(func() {
		common.RegisterEnabled = previousRegisterEnabled
		constant.GenerateDefaultToken = previousGenerateDefaultToken
	})

	user, err := resolveSocialOAuthUser(socialIdentity{
		Provider:    githubOAuthProvider,
		Subject:     "social-new-subject",
		Username:    "social-new",
		DisplayName: "新社交用户",
		Email:       "social-new@example.com",
	}, "")
	if err != nil {
		t.Fatalf("resolve social user: %v", err)
	}
	var token model.Token
	if err := model.CoreDB().Where("user_id = ?", user.Id).First(&token).Error; err != nil {
		t.Fatalf("load social default token: %v", err)
	}
	if !token.UnlimitedQuota || token.Name != "social-new的初始令牌" {
		t.Fatalf("unexpected social default token: %+v", token)
	}
}

func TestTruncateDisplayNameIsRuneSafe(t *testing.T) {
	input := strings.Repeat("界", 19) + "😀😀"
	got := truncateDisplayName(input)
	if !utf8.ValidString(got) {
		t.Fatalf("display name is invalid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != 20 {
		t.Fatalf("expected 20 runes, got %d in %q", utf8.RuneCountInString(got), got)
	}
	if !strings.HasSuffix(got, "😀") {
		t.Fatalf("expected truncation on a rune boundary, got %q", got)
	}
}

func TestVerifiedGitHubEmailPrefersPrimary(t *testing.T) {
	emails := []githubOAuthEmail{
		{Email: "secondary@example.com", Verified: true},
		{Email: "primary@example.com", Primary: true, Verified: true},
	}
	if got := verifiedGitHubEmail(emails); got != "primary@example.com" {
		t.Fatalf("unexpected verified email: %q", got)
	}
}
