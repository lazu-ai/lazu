package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

type oidcRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn oidcRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func setupOIDCTestDB(t *testing.T) {
	t.Helper()
	ResetAttempts()
	t.Cleanup(ResetAttempts)
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	previousDB := model.CoreDB()
	previousIssuer := common.OIDCIssuerURL
	previousAdminRoles := common.OIDCAdminRoles
	previousAutoCreate := common.OIDCAutoCreateUsers
	previousQuota := common.QuotaForNewUser
	previousRedirectURL := common.OIDCRedirectURL
	previousFrontendBaseURL := common.OIDCFrontendBaseURL
	previousPostLoginRedirectURL := common.OIDCPostLoginRedirectURL
	previousTrustedProxyCIDRs := common.TrustedProxyCIDRs
	model.SetCoreDB(db)
	common.OIDCIssuerURL = "https://issuer.example.com"
	common.OIDCAdminRoles = "admins,roots"
	common.OIDCAutoCreateUsers = true
	common.QuotaForNewUser = 0
	common.OIDCRedirectURL = ""
	common.OIDCFrontendBaseURL = ""
	common.OIDCPostLoginRedirectURL = "/console"
	t.Cleanup(func() {
		waitForAuthEmailDeliveries()
		model.SetCoreDB(previousDB)
		common.OIDCIssuerURL = previousIssuer
		common.OIDCAdminRoles = previousAdminRoles
		common.OIDCAutoCreateUsers = previousAutoCreate
		common.QuotaForNewUser = previousQuota
		common.OIDCRedirectURL = previousRedirectURL
		common.OIDCFrontendBaseURL = previousFrontendBaseURL
		common.OIDCPostLoginRedirectURL = previousPostLoginRedirectURL
		common.TrustedProxyCIDRs = previousTrustedProxyCIDRs
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	if err := db.AutoMigrate(&model.User{}, &model.UserIdentity{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}
}

func newOIDCRequestContext(host string, remoteAddr string) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/auth/oidc/login", nil)
	req.Host = host
	req.RemoteAddr = remoteAddr
	c.Request = req
	return c
}

func TestOIDCLoginPersistsNonceAndPKCE(t *testing.T) {
	setupOIDCTestDB(t)
	gin.SetMode(gin.TestMode)

	previousEnabled := common.OIDCEnabled
	previousClientID := common.OIDCClientID
	previousClientSecret := common.OIDCClientSecret
	previousRedirectURL := common.OIDCRedirectURL
	previousSessionSecret := common.SessionSecret
	common.OIDCEnabled = true
	common.OIDCClientID = "oidc-client"
	common.OIDCClientSecret = "placeholder-oidc-secret"
	common.OIDCRedirectURL = "https://console.example.com/api/auth/oidc/callback"
	common.SessionSecret = "0123456789abcdef0123456789abcdef" // secret-scan: allow -- test fixture
	t.Cleanup(func() {
		common.OIDCEnabled = previousEnabled
		common.OIDCClientID = previousClientID
		common.OIDCClientSecret = previousClientSecret
		common.OIDCRedirectURL = previousRedirectURL
		common.SessionSecret = previousSessionSecret
	})

	issuer := canonicalOIDCIssuer(common.OIDCIssuerURL)
	discovery := fmt.Sprintf(`{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`, issuer, issuer+"/authorize", issuer+"/token", issuer+"/jwks")
	client := &http.Client{Transport: oidcRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != issuer+"/.well-known/openid-configuration" {
			return nil, fmt.Errorf("unexpected OIDC discovery request: %s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(discovery)),
			Request:    request,
		}, nil
	})}
	requestContext := oidc.ClientContext(context.Background(), client)

	engine := gin.New()
	engine.GET("/oidc/login", OIDCLogin)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/oidc/login?next=%2Fconsole", nil).WithContext(requestContext)
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusFound {
		t.Fatalf("expected OIDC redirect, got status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse OIDC redirect: %v", err)
	}
	var transactionCookie *http.Cookie
	for _, responseCookie := range recorder.Result().Cookies() {
		if responseCookie.Name == oidcTransactionCookie {
			transactionCookie = responseCookie
			break
		}
	}
	if transactionCookie == nil {
		t.Fatal("OIDC transaction cookie was not set")
	}
	cookieValue, err := url.QueryUnescape(transactionCookie.Value)
	if err != nil {
		t.Fatalf("decode OIDC transaction cookie value: %v", err)
	}
	var tx oidcTransaction
	if err := oidcSecureCookie().Decode(oidcTransactionCookie, cookieValue, &tx); err != nil {
		t.Fatalf("decode OIDC transaction: %v", err)
	}
	if tx.State == "" || tx.Nonce == "" || tx.CodeVerifier == "" {
		t.Fatalf("OIDC transaction is incomplete: %+v", tx)
	}
	query := location.Query()
	if query.Get("state") != tx.State || query.Get("nonce") != tx.Nonce {
		t.Fatalf("redirect does not use persisted state/nonce: query=%v tx=%+v", query, tx)
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("expected S256 PKCE, got %q", query.Get("code_challenge_method"))
	}
	if query.Get("code_challenge") != oauth2.S256ChallengeFromVerifier(tx.CodeVerifier) {
		t.Fatal("redirect PKCE challenge does not match persisted verifier")
	}
	if tx.Next != "/console" {
		t.Fatalf("transaction did not retain redirect intent: %+v", tx)
	}
}

func TestValidOIDCTransactionRequiresStateNonceAndPKCE(t *testing.T) {
	valid := oidcTransaction{State: "state", Nonce: "nonce", CodeVerifier: "verifier"}
	tests := []struct {
		name  string
		tx    oidcTransaction
		state string
		want  bool
	}{
		{name: "valid", tx: valid, state: "state", want: true},
		{name: "missing state", tx: oidcTransaction{Nonce: "nonce", CodeVerifier: "verifier"}, state: "state"},
		{name: "missing nonce", tx: oidcTransaction{State: "state", CodeVerifier: "verifier"}, state: "state"},
		{name: "missing verifier", tx: oidcTransaction{State: "state", Nonce: "nonce"}, state: "state"},
		{name: "state mismatch", tx: valid, state: "other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validOIDCTransaction(test.tx, test.state); got != test.want {
				t.Fatalf("validOIDCTransaction()=%v, want %v", got, test.want)
			}
		})
	}
}

func TestOIDCNamespacedSubjectIncludesIssuer(t *testing.T) {
	first := oidcNamespacedSubject("https://issuer.example.com/", "same-subject")
	canonical := oidcNamespacedSubject("https://issuer.example.com", "same-subject")
	second := oidcNamespacedSubject("https://other.example.com", "same-subject")
	if first != canonical {
		t.Fatalf("issuer trailing slash changed identity key: %q != %q", first, canonical)
	}
	if first == second {
		t.Fatal("the same subject at different issuers collided")
	}
	if !strings.HasPrefix(first, "v1:") || len(first) > 255 {
		t.Fatalf("unexpected namespaced subject format: %q", first)
	}
}

func TestRequestBaseURLRejectsUntrustedPublicHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousTrustedProxyCIDRs := common.TrustedProxyCIDRs
	common.TrustedProxyCIDRs = "10.0.0.0/24"
	t.Cleanup(func() {
		common.TrustedProxyCIDRs = previousTrustedProxyCIDRs
	})

	c := newOIDCRequestContext("attacker.example.com", "198.51.100.10:4321")
	c.Request.Header.Set("X-Forwarded-Host", "app.example.com")
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	if _, err := requestBaseURL(c); err == nil {
		t.Fatalf("expected untrusted public host to require OIDC_REDIRECT_URL")
	}
	if requestIsSecure(c) {
		t.Fatalf("untrusted forwarded proto should not mark the request secure")
	}
}

func TestRequestBaseURLUsesTrustedForwardedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousTrustedProxyCIDRs := common.TrustedProxyCIDRs
	common.TrustedProxyCIDRs = "10.0.0.0/24"
	t.Cleanup(func() {
		common.TrustedProxyCIDRs = previousTrustedProxyCIDRs
	})

	c := newOIDCRequestContext("internal:3000", "10.0.0.8:4321")
	c.Request.Header.Set("X-Forwarded-Host", "app.example.com")
	c.Request.Header.Set("X-Forwarded-Proto", "https")

	got, err := requestBaseURL(c)
	if err != nil {
		t.Fatalf("requestBaseURL error: %v", err)
	}
	if got != "https://app.example.com" {
		t.Fatalf("unexpected base URL: %q", got)
	}
	if !requestIsSecure(c) {
		t.Fatalf("trusted forwarded proto should mark the request secure")
	}
}

func TestRequestBaseURLAllowsLocalDevHost(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousTrustedProxyCIDRs := common.TrustedProxyCIDRs
	common.TrustedProxyCIDRs = "10.0.0.0/24"
	t.Cleanup(func() {
		common.TrustedProxyCIDRs = previousTrustedProxyCIDRs
	})

	c := newOIDCRequestContext("localhost:3000", "198.51.100.10:4321")
	got, err := requestBaseURL(c)
	if err != nil {
		t.Fatalf("requestBaseURL error: %v", err)
	}
	if got != "http://localhost:3000" {
		t.Fatalf("unexpected local dev base URL: %q", got)
	}
}

func TestOIDCFrontendCallbackUsesConfiguredFrontendOrigin(t *testing.T) {
	setupOIDCTestDB(t)
	common.OIDCFrontendBaseURL = "https://console.example.com"

	got := oidcFrontendCallbackURL("/console")
	want := "https://console.example.com/auth/oidc/callback?next=%2Fconsole"
	if got != want {
		t.Fatalf("unexpected frontend callback URL: got %q want %q", got, want)
	}
}

func TestOIDCFrontendCallbackFallsBackToSharedFrontendOrigin(t *testing.T) {
	setupOIDCTestDB(t)
	t.Setenv("FRONTEND_BASE_URL", "https://console.example.com")

	got := oidcFrontendCallbackURL("/console")
	want := "https://console.example.com/auth/oidc/callback?next=%2Fconsole"
	if got != want {
		t.Fatalf("unexpected shared frontend callback URL: got %q want %q", got, want)
	}
}

func TestOIDCPostLoginAbsoluteURLSuppliesOriginAndPath(t *testing.T) {
	setupOIDCTestDB(t)
	common.OIDCPostLoginRedirectURL = "https://console.example.com/console?tab=login"

	if next := sanitizedOIDCNext(""); next != "/console?tab=login" {
		t.Fatalf("expected absolute post-login URL to sanitize to path+query, got %q", next)
	}
	got := oidcFrontendCallbackURL("")
	want := "https://console.example.com/auth/oidc/callback?next=%2Fconsole%3Ftab%3Dlogin"
	if got != want {
		t.Fatalf("unexpected callback URL: got %q want %q", got, want)
	}
}

func TestSanitizedOIDCNextRejectsBrowserNormalizedExternalPaths(t *testing.T) {
	for _, candidate := range []string{
		`//evil.example/steal`,
		`/\\evil.example/steal`,
		"/\n/evil.example/steal",
		"/\t/evil.example/steal",
	} {
		t.Run(fmt.Sprintf("%q", candidate), func(t *testing.T) {
			if got := sanitizedOIDCNext(candidate); got != "/console" {
				t.Fatalf("unsafe post-login path was accepted: got %q", got)
			}
		})
	}
}

func TestResolveOIDCUserCreatesUserAndMapsAdminRole(t *testing.T) {
	setupOIDCTestDB(t)
	if err := model.CoreDB().AutoMigrate(&model.Token{}); err != nil {
		t.Fatalf("migrate signup features: %v", err)
	}
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	constant.GenerateDefaultToken = true
	previousDeliver := deliverAuthEmail
	welcomeEmails := make(chan string, 1)
	deliverAuthEmail = func(_ string, receiver string, _ string) error {
		welcomeEmails <- receiver
		return nil
	}
	t.Cleanup(func() {
		waitForAuthEmailDeliveries()
		deliverAuthEmail = previousDeliver
		constant.GenerateDefaultToken = previousGenerateDefaultToken
	})
	user, err := resolveOIDCUser(context.Background(), "sub-123", map[string]any{
		"email":              "sso@example.com",
		"email_verified":     true,
		"preferred_username": "sso-user",
		"name":               "SSO User",
		"groups":             []any{"admins"},
	})
	if err != nil {
		t.Fatalf("resolve oidc user: %v", err)
	}
	if user.Username != "sso-user" || user.Email != "sso@example.com" || user.DisplayName != "SSO User" {
		t.Fatalf("unexpected user fields: %+v", user)
	}
	if user.Role != common.RoleRootUser {
		t.Fatalf("expected admin role, got %d", user.Role)
	}
	if !user.GetSetting().NeedsPassword {
		t.Fatal("new OIDC account must be marked as needing a user-chosen password")
	}
	var identity model.UserIdentity
	if err := model.CoreDB().Where("user_id = ? AND provider = ?", user.Id, "oidc").First(&identity).Error; err != nil {
		t.Fatalf("load OIDC identity: %v", err)
	}
	if identity.Subject != oidcNamespacedSubject(common.OIDCIssuerURL, "sub-123") || identity.Email != "sso@example.com" {
		t.Fatalf("unexpected OIDC identity: %+v", identity)
	}
	var token model.Token
	if err := model.CoreDB().Where("user_id = ?", user.Id).First(&token).Error; err != nil {
		t.Fatalf("load default OIDC token: %v", err)
	}
	if !token.UnlimitedQuota || token.Name != "sso-user的初始令牌" {
		t.Fatalf("unexpected default OIDC token: %+v", token)
	}
	waitForAuthEmailDeliveries()
	select {
	case receiver := <-welcomeEmails:
		if receiver != user.Email {
			t.Fatalf("OIDC welcome email sent to %q, want %q", receiver, user.Email)
		}
	default:
		t.Fatal("new OIDC account did not enqueue a welcome email")
	}
}

func TestConcurrentOIDCRegistrationsSerializeUsernameAllocation(t *testing.T) {
	setupOIDCTestDB(t)
	defer waitForAuthEmailDeliveries()
	previousGenerateDefaultToken := constant.GenerateDefaultToken
	constant.GenerateDefaultToken = false
	t.Cleanup(func() { constant.GenerateDefaultToken = previousGenerateDefaultToken })

	start := make(chan struct{})
	users := make([]*model.User, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range users {
		index := index
		go func() {
			defer wait.Done()
			<-start
			users[index], errs[index] = resolveOIDCUser(context.Background(), fmt.Sprintf("shared-name-subject-%d", index), map[string]any{
				"email":              fmt.Sprintf("shared-name-%d@example.com", index),
				"email_verified":     true,
				"preferred_username": "shared-name",
			})
		}()
	}
	close(start)
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent OIDC registration %d failed: %v", index, err)
		}
	}
	if users[0] == nil || users[1] == nil || users[0].Id == users[1].Id {
		t.Fatalf("expected two distinct OIDC users, got %+v", users)
	}
	usernames := map[string]bool{users[0].Username: true, users[1].Username: true}
	if !usernames["shared-name"] || !usernames["shared-name-1"] {
		t.Fatalf("concurrent username allocation did not converge on unique candidates: %+v", usernames)
	}
}

func TestResolveOIDCUserBindsExistingEmailAndMapsRootRole(t *testing.T) {
	setupOIDCTestDB(t)
	existing := model.User{
		Username: "existing",
		Password: "password123",
		Email:    "existing@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&existing).Error; err != nil {
		t.Fatalf("seed existing user: %v", err)
	}

	user, err := resolveOIDCUser(context.Background(), "sub-existing", map[string]any{
		"email":              "existing@example.com",
		"email_verified":     true,
		"preferred_username": "ignored-name",
		"groups":             []any{"roots"},
	})
	if err != nil {
		t.Fatalf("resolve oidc user: %v", err)
	}
	if user.Id != existing.Id {
		t.Fatalf("expected existing user id %d, got %d", existing.Id, user.Id)
	}
	if user.Role != common.RoleRootUser {
		t.Fatalf("expected root role, got %d", user.Role)
	}
	var identity model.UserIdentity
	if err := model.CoreDB().Where("user_id = ? AND provider = ?", user.Id, oidcIdentityProvider).First(&identity).Error; err != nil {
		t.Fatalf("load bound OIDC identity: %v", err)
	}
	if identity.Subject != oidcNamespacedSubject(common.OIDCIssuerURL, "sub-existing") {
		t.Fatalf("expected oidc subject to be bound, got %+v", identity)
	}
}

func TestResolveOIDCUserDowngradesWhenRoleClaimsRemoved(t *testing.T) {
	setupOIDCTestDB(t)
	existing := model.User{
		Username:       "oidc-admin",
		Password:       "password123",
		Email:          "oidc-admin@example.com",
		Role:           common.RoleRootUser,
		Status:         common.UserStatusEnabled,
		SessionVersion: 4,
	}
	if err := model.CoreDB().Create(&existing).Error; err != nil {
		t.Fatalf("seed existing oidc user: %v", err)
	}
	seedOIDCIdentity(t, existing, "sub-admin")

	user, err := resolveOIDCUser(context.Background(), "sub-admin", map[string]any{
		"email":              "oidc-admin@example.com",
		"email_verified":     true,
		"preferred_username": "oidc-admin",
		"groups":             []any{},
	})
	if err != nil {
		t.Fatalf("resolve oidc user: %v", err)
	}
	if user.Role != common.RoleCommonUser {
		t.Fatalf("expected removed oidc role claims to downgrade user, got %d", user.Role)
	}
	if user.SessionVersion != existing.SessionVersion+1 {
		t.Fatalf("role downgrade returned stale session version %d", user.SessionVersion)
	}
	var stored model.User
	if err := model.CoreDB().First(&stored, existing.Id).Error; err != nil {
		t.Fatalf("reload oidc user: %v", err)
	}
	if stored.Role != common.RoleCommonUser {
		t.Fatalf("expected downgraded role to persist, got %d", stored.Role)
	}
	if stored.SessionVersion != existing.SessionVersion+1 {
		t.Fatalf("role downgrade did not rotate sessions: got %d", stored.SessionVersion)
	}
}

func TestOIDCRoleChangeRotatesOldSessionAndIssuesCurrentVersion(t *testing.T) {
	setupOIDCTestDB(t)
	gin.SetMode(gin.TestMode)
	existing := model.User{
		Username:       "oidc-versioned-admin",
		Password:       "password123",
		Email:          "oidc-versioned-admin@example.com",
		Role:           common.RoleRootUser,
		Status:         common.UserStatusEnabled,
		SessionVersion: 7,
	}
	if err := model.CoreDB().Create(&existing).Error; err != nil {
		t.Fatalf("seed versioned OIDC user: %v", err)
	}
	if err := model.CoreDB().Create(&model.UserIdentity{
		UserID: existing.Id, Provider: oidcIdentityProvider,
		Subject: oidcNamespacedSubject(common.OIDCIssuerURL, "sub-versioned-admin"), Email: existing.Email,
	}).Error; err != nil {
		t.Fatalf("seed versioned OIDC identity: %v", err)
	}

	store := cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))
	engine := gin.New()
	engine.Use(sessions.Sessions("session", store))
	engine.GET("/seed-old", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", existing.Id)
		session.Set("session_version", existing.SessionVersion)
		if err := session.Save(); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusNoContent)
	})
	engine.GET("/oidc-callback-session", func(c *gin.Context) {
		user, err := resolveOIDCUser(c.Request.Context(), "sub-versioned-admin", map[string]any{
			"email":          existing.Email,
			"email_verified": true,
			"groups":         []any{},
		})
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		setupLogin(user, c)
	})
	engine.GET("/bootstrap", OIDCBootstrap)

	oldRecorder := httptest.NewRecorder()
	engine.ServeHTTP(oldRecorder, httptest.NewRequest(http.MethodGet, "/seed-old", nil))
	if oldRecorder.Code != http.StatusNoContent || len(oldRecorder.Result().Cookies()) == 0 {
		t.Fatalf("seed old session: status=%d cookies=%v", oldRecorder.Code, oldRecorder.Result().Cookies())
	}
	oldCookie := oldRecorder.Result().Cookies()[0]

	callbackRecorder := httptest.NewRecorder()
	engine.ServeHTTP(callbackRecorder, httptest.NewRequest(http.MethodGet, "/oidc-callback-session", nil))
	if callbackRecorder.Code != http.StatusOK || !strings.Contains(callbackRecorder.Body.String(), `"success":true`) {
		t.Fatalf("issue callback session: status=%d body=%s", callbackRecorder.Code, callbackRecorder.Body.String())
	}
	if len(callbackRecorder.Result().Cookies()) == 0 {
		t.Fatal("OIDC callback session cookie was not issued")
	}
	currentCookie := callbackRecorder.Result().Cookies()[0]

	var stored model.User
	if err := model.CoreDB().First(&stored, existing.Id).Error; err != nil {
		t.Fatalf("reload role-changed user: %v", err)
	}
	if stored.Role != common.RoleCommonUser || stored.SessionVersion != existing.SessionVersion+1 {
		t.Fatalf("role and session version were not changed atomically: role=%d version=%d", stored.Role, stored.SessionVersion)
	}

	oldRequest := httptest.NewRequest(http.MethodGet, "/bootstrap", nil)
	oldRequest.AddCookie(oldCookie)
	oldBootstrap := httptest.NewRecorder()
	engine.ServeHTTP(oldBootstrap, oldRequest)
	if oldBootstrap.Code != http.StatusUnauthorized || !strings.Contains(oldBootstrap.Body.String(), `"code":"session_expired"`) {
		t.Fatalf("pre-role-change cookie remained valid: status=%d body=%s", oldBootstrap.Code, oldBootstrap.Body.String())
	}

	currentRequest := httptest.NewRequest(http.MethodGet, "/bootstrap", nil)
	currentRequest.AddCookie(currentCookie)
	currentBootstrap := httptest.NewRecorder()
	engine.ServeHTTP(currentBootstrap, currentRequest)
	if currentBootstrap.Code != http.StatusOK || !strings.Contains(currentBootstrap.Body.String(), `"success":true`) {
		t.Fatalf("callback cookie did not carry new session version: status=%d body=%s", currentBootstrap.Code, currentBootstrap.Body.String())
	}
}

func TestResolveOIDCUserPreservesExistingRoleWithoutRoleMappings(t *testing.T) {
	setupOIDCTestDB(t)
	common.OIDCAdminRoles = ""
	users := []model.User{
		{
			Username:       "oidc-admin-unmapped",
			Password:       "password123",
			Email:          "oidc-admin-unmapped@example.com",
			Role:           common.RoleRootUser,
			Status:         common.UserStatusEnabled,
			SessionVersion: 3,
		},
		{
			Username:       "oidc-root-unmapped",
			Password:       "password123",
			Email:          "oidc-root-unmapped@example.com",
			Role:           common.RoleRootUser,
			Status:         common.UserStatusEnabled,
			SessionVersion: 5,
		},
	}
	subjects := []string{"sub-admin-unmapped", "sub-root-unmapped"}
	for i := range users {
		if err := model.CoreDB().Create(&users[i]).Error; err != nil {
			t.Fatalf("seed existing oidc user: %v", err)
		}
		seedOIDCIdentity(t, users[i], subjects[i])
	}

	for i, existing := range users {
		user, err := resolveOIDCUser(context.Background(), subjects[i], map[string]any{
			"email":              existing.Email,
			"email_verified":     true,
			"preferred_username": existing.Username,
			"groups":             []any{},
		})
		if err != nil {
			t.Fatalf("resolve oidc user %s: %v", existing.Username, err)
		}
		if user.Role != existing.Role {
			t.Fatalf("expected existing role %d to be preserved, got %d", existing.Role, user.Role)
		}
		if user.SessionVersion != existing.SessionVersion {
			t.Fatalf("unchanged role rotated session version from %d to %d", existing.SessionVersion, user.SessionVersion)
		}
		var stored model.User
		if err := model.CoreDB().First(&stored, existing.Id).Error; err != nil {
			t.Fatalf("reload oidc user %s: %v", existing.Username, err)
		}
		if stored.Role != existing.Role {
			t.Fatalf("expected stored role %d to be preserved, got %d", existing.Role, stored.Role)
		}
		if stored.SessionVersion != existing.SessionVersion {
			t.Fatalf("stored session version changed without a role change: got %d", stored.SessionVersion)
		}
	}
}

func TestResolveOIDCUserDoesNotBindByUsernameOrUnverifiedEmail(t *testing.T) {
	setupOIDCTestDB(t)
	existing := model.User{
		Username: "root",
		Password: "password123",
		Email:    "root@example.com",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&existing).Error; err != nil {
		t.Fatalf("seed existing user: %v", err)
	}

	user, err := resolveOIDCUser(context.Background(), "sub-new", map[string]any{
		"email":              "root@example.com",
		"email_verified":     false,
		"preferred_username": "root",
		"groups":             []any{"admins"},
	})
	if err != nil {
		t.Fatalf("resolve oidc user: %v", err)
	}
	if user.Id == existing.Id {
		t.Fatalf("did not expect OIDC user to bind existing account by username or unverified email")
	}
	if user.Username != "root-1" {
		t.Fatalf("expected unique username allocation, got %q", user.Username)
	}
	if user.Email != "" {
		t.Fatalf("expected unverified email to be omitted, got %q", user.Email)
	}
}

func TestFindExistingOIDCUserPrefersIdentityOverVerifiedEmail(t *testing.T) {
	setupOIDCTestDB(t)
	issuer := canonicalOIDCIssuer(common.OIDCIssuerURL)
	subject := "shared-subject"
	identityOwner := model.User{
		Username: "identity-owner", Password: "password123", Email: "owner@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	legacyOwner := model.User{
		Username: "legacy-owner", Password: "password123", Email: "legacy@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&identityOwner).Error; err != nil {
		t.Fatalf("seed identity owner: %v", err)
	}
	if err := model.CoreDB().Create(&legacyOwner).Error; err != nil {
		t.Fatalf("seed legacy owner: %v", err)
	}
	if err := model.CoreDB().Create(&model.UserIdentity{
		UserID: identityOwner.Id, Provider: oidcIdentityProvider,
		Subject: oidcNamespacedSubject(issuer, subject), Email: identityOwner.Email,
	}).Error; err != nil {
		t.Fatalf("seed exact identity: %v", err)
	}

	found, err := findExistingOIDCUser(issuer, subject, legacyOwner.Email)
	if err != nil {
		t.Fatalf("find OIDC user: %v", err)
	}
	if found == nil || found.Id != identityOwner.Id {
		t.Fatalf("expected identity owner %d, got %+v", identityOwner.Id, found)
	}
}

func TestResolveOIDCUserRollsBackOnVerifiedEmailConflict(t *testing.T) {
	setupOIDCTestDB(t)
	issuer := canonicalOIDCIssuer(common.OIDCIssuerURL)
	subject := "rollback-subject"
	owner := model.User{
		Username: "rollback-owner", DisplayName: "Before", Password: "password123",
		Email: "owner@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	conflicting := model.User{
		Username: "email-owner", Password: "password123", Email: "claimed@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&owner).Error; err != nil {
		t.Fatalf("seed OIDC owner: %v", err)
	}
	if err := model.CoreDB().Create(&conflicting).Error; err != nil {
		t.Fatalf("seed email owner: %v", err)
	}
	identity := model.UserIdentity{
		UserID: owner.Id, Provider: oidcIdentityProvider,
		Subject: oidcNamespacedSubject(issuer, subject), Email: owner.Email,
	}
	if err := model.CoreDB().Create(&identity).Error; err != nil {
		t.Fatalf("seed OIDC identity: %v", err)
	}

	_, err := resolveOIDCUser(context.Background(), subject, map[string]any{
		"email":          conflicting.Email,
		"email_verified": true,
		"name":           "After",
		"groups":         []any{"roots"},
	})
	if !errors.Is(err, errOAuthEmailConflict) {
		t.Fatalf("expected verified-email conflict, got %v", err)
	}
	var storedOwner model.User
	if err := model.CoreDB().First(&storedOwner, owner.Id).Error; err != nil {
		t.Fatalf("reload OIDC owner: %v", err)
	}
	if storedOwner.Email != owner.Email || storedOwner.DisplayName != owner.DisplayName || storedOwner.Role != owner.Role {
		t.Fatalf("OIDC owner was partially mutated after rollback: %+v", storedOwner)
	}
	var storedIdentity model.UserIdentity
	if err := model.CoreDB().First(&storedIdentity, identity.ID).Error; err != nil {
		t.Fatalf("reload OIDC identity: %v", err)
	}
	if storedIdentity.Email != identity.Email || storedIdentity.Subject != identity.Subject {
		t.Fatalf("OIDC identity was partially mutated after rollback: %+v", storedIdentity)
	}
}

func TestResolveOIDCUserDoesNotRebindExistingIdentityByEmail(t *testing.T) {
	setupOIDCTestDB(t)
	issuer := canonicalOIDCIssuer(common.OIDCIssuerURL)
	user := model.User{
		Username: "stable-identity", DisplayName: "Before", Password: "password123",
		Email: "stable@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed OIDC user: %v", err)
	}
	identity := model.UserIdentity{
		UserID: user.Id, Provider: oidcIdentityProvider,
		Subject: oidcNamespacedSubject(issuer, "original-subject"), Email: user.Email,
	}
	if err := model.CoreDB().Create(&identity).Error; err != nil {
		t.Fatalf("seed OIDC identity: %v", err)
	}

	_, err := resolveOIDCUser(context.Background(), "replacement-subject", map[string]any{
		"email": user.Email, "email_verified": true, "name": "After", "groups": []any{"roots"},
	})
	if !errors.Is(err, errOAuthIdentityConflict) {
		t.Fatalf("expected identity conflict, got %v", err)
	}
	var storedUser model.User
	if err := model.CoreDB().First(&storedUser, user.Id).Error; err != nil {
		t.Fatalf("reload OIDC user: %v", err)
	}
	if storedUser.DisplayName != user.DisplayName || storedUser.Role != user.Role {
		t.Fatalf("OIDC user was partially rebound: %+v", storedUser)
	}
	var storedIdentity model.UserIdentity
	if err := model.CoreDB().First(&storedIdentity, identity.ID).Error; err != nil {
		t.Fatalf("reload stable identity: %v", err)
	}
	if storedIdentity.Subject != identity.Subject {
		t.Fatalf("OIDC identity was rebound by email: %+v", storedIdentity)
	}
}

func TestOIDCBootstrapRejectsStaleSessionVersion(t *testing.T) {
	setupOIDCTestDB(t)
	gin.SetMode(gin.TestMode)
	user := model.User{
		Username: "stale-bootstrap", Password: "password123", Email: "stale-bootstrap@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SessionVersion: 2,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed versioned user: %v", err)
	}

	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))))
	engine.Use(func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set("session_version", 1)
		if err := session.Save(); err != nil {
			t.Fatalf("seed stale session: %v", err)
		}
		c.Next()
	})
	engine.GET("/bootstrap", OIDCBootstrap)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/bootstrap", nil))
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"session_expired"`) {
		t.Fatalf("stale OIDC bootstrap session was accepted: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestLoginAllowsOnlyRootBreakGlassWhenPasswordLoginDisabled(t *testing.T) {
	setupOIDCTestDB(t)
	previousPasswordLogin := common.PasswordLoginEnabled
	previousBreakGlass := common.OIDCBreakGlassPasswordEnabled
	common.PasswordLoginEnabled = false
	common.OIDCBreakGlassPasswordEnabled = true
	t.Cleanup(func() {
		common.PasswordLoginEnabled = previousPasswordLogin
		common.OIDCBreakGlassPasswordEnabled = previousBreakGlass
	})

	root := model.User{Username: "owner", Password: "password123", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	if err := root.Insert(0); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	regular := model.User{Username: "regular", Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if err := regular.Insert(0); err != nil {
		t.Fatalf("seed regular: %v", err)
	}

	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("secret"))))
	engine.POST("/login", Login)

	rootBody, _ := json.Marshal(LoginRequest{Username: "owner", Password: "password123"})
	rootRecorder := httptest.NewRecorder()
	engine.ServeHTTP(rootRecorder, httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(rootBody)))
	if !strings.Contains(rootRecorder.Body.String(), `"success":true`) {
		t.Fatalf("expected custom root break-glass login to succeed, got %s", rootRecorder.Body.String())
	}

	regularBody, _ := json.Marshal(LoginRequest{Username: "regular", Password: "password123"})
	regularRecorder := httptest.NewRecorder()
	engine.ServeHTTP(regularRecorder, httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(regularBody)))
	// A disabled password door answers exactly like a wrong password, so the
	// break-glass exception reveals nothing about which accounts are root.
	requireUserAPIError(t, regularRecorder, http.StatusUnauthorized, types.ErrorCodeInvalidCredentials)

	regularWrongBody, _ := json.Marshal(LoginRequest{Username: "regular", Password: "wrong-password"})
	regularWrongRecorder := httptest.NewRecorder()
	engine.ServeHTTP(regularWrongRecorder, httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(regularWrongBody)))
	requireUserAPIError(t, regularWrongRecorder, http.StatusUnauthorized, types.ErrorCodeInvalidCredentials)
}

// seedOIDCIdentity gives an existing account the namespaced OIDC identity the
// resolver looks up first. Since schema version 15 this row is the only place
// an OIDC identity lives.
func seedOIDCIdentity(t *testing.T, user model.User, subject string) {
	t.Helper()
	if err := model.CoreDB().Create(&model.UserIdentity{
		UserID: user.Id, Provider: oidcIdentityProvider,
		Subject: oidcNamespacedSubject(common.OIDCIssuerURL, subject), Email: user.Email,
	}).Error; err != nil {
		t.Fatalf("seed OIDC identity: %v", err)
	}
}
