package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/requestpath"
	"gorm.io/gorm"
)

var errAuthoritySQLFixture = errors.New("injected authority SQL failure")

type authorityFixture struct {
	db         *gorm.DB
	user       model.User
	token      model.Token
	internal   model.Token
	credential model.EphemeralCredential
	key        string
}

// authorityHiddenTokenKind stands for a credential an extension creates for
// itself; the core must treat it as internal.
var authorityHiddenTokenKind = model.HiddenTokenKind{Product: "test-extension", Name: "test-runner"}

func init() {
	model.RegisterHiddenTokenKind(authorityHiddenTokenKind)
}

func newAuthorityFixture(t *testing.T) authorityFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousDB, previousRedisEnabled := model.CoreDB(), common.RedisEnabled
	db, err := gorm.Open(gormsqlite.Open("file:"+common.GeneratePublicID("authority")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.SetCoreDB(db)
	common.RedisEnabled = false
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
		common.RedisEnabled = previousRedisEnabled
	})
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.Project{}, &model.ProjectMember{}, &model.EphemeralCredential{}, &model.EphemeralCredentialAudit{}); err != nil {
		t.Fatal(err)
	}
	fixture := authorityFixture{
		db:       db,
		user:     model.User{Id: 1, Username: "authority-fixture", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, SessionVersion: 3},
		token:    model.Token{Key: common.GeneratePublicID("regular_fixture"), Id: 2, UserId: 1, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
		internal: model.Token{Key: common.GeneratePublicID("internal_fixture"), Id: 3, UserId: 1, Name: authorityHiddenTokenKind.Name, Product: authorityHiddenTokenKind.Product, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true},
		key:      model.EphemeralCredentialPrefix + common.GeneratePublicID("fixture"),
	}
	fixture.credential = model.EphemeralCredential{ID: 4, UserID: fixture.user.Id, TokenID: fixture.token.Id, CredentialDigest: common.GenerateHMAC(fixture.key), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := db.Create(&fixture.user).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", fixture.user.Id).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	fixture.token.ProjectID, fixture.internal.ProjectID = project.ID, project.ID
	for _, row := range []any{&fixture.token, &fixture.internal, &fixture.credential} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func injectAuthoritySQLFailure(t *testing.T, db *gorm.DB, table string) *int {
	t.Helper()
	calls := 0
	if err := db.Callback().Query().Before("gorm:query").Register("test:authority-query", func(tx *gorm.DB) {
		if tx.Statement.Table == table {
			calls++
			tx.AddError(errAuthoritySQLFixture)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:authority-query") })
	return &calls
}

func assertAuthorityUnavailable(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("authority outage returned status=%d retry=%q body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if consoleErrorCode(payload) != "gateway_overloaded" {
		t.Fatalf("authority outage misclassified: %s", response.Body.String())
	}
}

func TestTokenAuthAuthoritySQLOutageFailsClosed(t *testing.T) {
	previousSecret := common.SessionSecret
	common.SessionSecret = "authority-signing-fixture" // secret-scan: allow (test fixture)
	t.Setenv("INTERNAL_PROXY_SECRET", "")
	t.Cleanup(func() { common.SessionSecret = previousSecret })
	for _, name := range []string{"internal_token", "internal_ephemeral", "external_ephemeral", "ephemeral_parent", "session_token"} {
		t.Run(name, func(t *testing.T) {
			fixture := newAuthorityFixture(t)
			table := "tokens"
			if name == "internal_ephemeral" || name == "external_ephemeral" {
				table = "ephemeral_credentials"
			}
			calls := injectAuthoritySQLFailure(t, fixture.db, table)
			request := httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			var identity *common.InternalRelayIdentity
			contextID := 0
			switch name {
			case "internal_token", "internal_ephemeral":
				value := common.InternalRelayIdentity{TokenID: fixture.token.Id, UserID: fixture.user.Id}
				if name == "internal_ephemeral" {
					value.EphemeralID = fixture.credential.ID
				}
				identity = &value
				request = request.WithContext(common.WithInternalRelayIdentity(request.Context(), value))
				if err := SignInternalProxyRequest(request, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "external_ephemeral", "ephemeral_parent":
				request.Header.Set("Authorization", "Bearer "+fixture.key)
			case "session_token":
				request.Header.Set("X-Token-Id", strconv.Itoa(fixture.token.Id))
				contextID = fixture.user.Id
			}
			// Prove the typed marker retains the original driver error, including
			// paths that previously replaced it with an invalid-credential string.
			var lookupErr error
			context, _ := newGinTestContext()
			context.Request = request
			context.Set("id", contextID)
			switch name {
			case "internal_token":
				_, lookupErr = requestpath.Current().TokenByID(identity.TokenID)
			case "internal_ephemeral":
				_, lookupErr = requestpath.Current().EphemeralByID(request.Context(), identity.EphemeralID)
			case "external_ephemeral", "ephemeral_parent":
				_, _, lookupErr = model.ValidateEphemeralCredential(fixture.key)
			case "session_token":
				_, _, lookupErr = resolveTokenByIDHeader(context)
			}
			if !errors.Is(lookupErr, model.ErrIdentityAuthorityUnavailable) || !errors.Is(lookupErr, errAuthoritySQLFixture) {
				t.Fatalf("authority error lost classification/cause: %v", lookupErr)
			}
			router := gin.New()
			router.Use(InternalProxyAuth(), sessions.Sessions("authority-session", cookie.NewStore([]byte("authority-cookie-fixture"))))
			if contextID > 0 {
				router.Use(func(c *gin.Context) { c.Set("id", contextID); c.Next() })
			}
			handled := false
			router.POST("/v1/videos", TokenAuth(), func(c *gin.Context) { handled = true; c.Status(http.StatusOK) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertAuthorityUnavailable(t, response)
			if handled || *calls < 2 {
				t.Fatalf("authority failure allowed handler or missed injection: handled=%v calls=%d", handled, *calls)
			}
		})
	}
}

func TestTokenAuthMissingAndInactiveCredentialsKeepContract(t *testing.T) {
	previousSecret := common.SessionSecret
	common.SessionSecret = "authority-signing-fixture" // secret-scan: allow (test fixture)
	t.Setenv("INTERNAL_PROXY_SECRET", "")
	t.Cleanup(func() { common.SessionSecret = previousSecret })
	for _, name := range []string{"missing_internal_token", "missing_internal_ephemeral", "missing_ephemeral", "missing_ephemeral_parent", "expired_ephemeral", "revoked_ephemeral", "expired_parent", "disabled_parent", "missing_session_token", "expired_session_token", "disabled_session_token", "exhausted_session_token", "exhausted_parent", "untrusted_expired_internal_session", "untrusted_exhausted_internal_session"} {
		t.Run(name, func(t *testing.T) {
			fixture := newAuthorityFixture(t)
			request := httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			contextID := 0
			var changeErr error
			switch name {
			case "missing_internal_token", "missing_internal_ephemeral":
				value := common.InternalRelayIdentity{TokenID: fixture.token.Id, UserID: fixture.user.Id}
				if name == "missing_internal_token" {
					value.TokenID = 999
				} else {
					value.EphemeralID = 999
				}
				request = request.WithContext(common.WithInternalRelayIdentity(request.Context(), value))
			case "missing_ephemeral":
				request.Header.Set("Authorization", "Bearer "+model.EphemeralCredentialPrefix+common.GeneratePublicID("missing"))
			case "missing_session_token":
				request.Header.Set("X-Token-Id", "999")
				contextID = fixture.user.Id
			case "untrusted_expired_internal_session", "untrusted_exhausted_internal_session":
				request.Header.Set("X-Token-Id", strconv.Itoa(fixture.internal.Id))
				contextID = fixture.user.Id
				column, value := "expired_time", time.Now().Add(-time.Hour).Unix()
				if name == "untrusted_exhausted_internal_session" {
					column, value = "status", int64(common.TokenStatusExhausted)
				}
				changeErr = fixture.db.Model(&model.Token{}).Where("id = ?", fixture.internal.Id).Update(column, value).Error
			case "expired_session_token", "disabled_session_token", "exhausted_session_token":
				tokenID := fixture.token.Id
				request.Header.Set("X-Token-Id", strconv.Itoa(tokenID))
				contextID = fixture.user.Id
				column, value := "expired_time", time.Now().Add(-time.Hour).Unix()
				if name == "disabled_session_token" {
					column, value = "status", int64(common.TokenStatusDisabled)
				}
				if name == "exhausted_session_token" {
					column, value = "status", int64(common.TokenStatusExhausted)
				}
				changeErr = fixture.db.Model(&model.Token{}).Where("id = ?", tokenID).Update(column, value).Error
			default:
				request.Header.Set("Authorization", "Bearer "+fixture.key)
				switch name {
				case "missing_ephemeral_parent":
					changeErr = fixture.db.Delete(&model.Token{}, fixture.token.Id).Error
				case "expired_ephemeral":
					changeErr = fixture.db.Model(&model.EphemeralCredential{}).Where("id = ?", fixture.credential.ID).Update("expires_at", time.Now().Add(-time.Hour).Unix()).Error
				case "revoked_ephemeral":
					changeErr = fixture.db.Model(&model.EphemeralCredential{}).Where("id = ?", fixture.credential.ID).Update("revoked_at", time.Now().Unix()).Error
				case "expired_parent":
					changeErr = fixture.db.Model(&model.Token{}).Where("id = ?", fixture.token.Id).Update("expired_time", time.Now().Add(-time.Hour).Unix()).Error
				case "disabled_parent":
					changeErr = fixture.db.Model(&model.Token{}).Where("id = ?", fixture.token.Id).Update("status", common.TokenStatusDisabled).Error
				case "exhausted_parent":
					changeErr = fixture.db.Model(&model.Token{}).Where("id = ?", fixture.token.Id).Update("status", common.TokenStatusExhausted).Error
				}
			}
			if changeErr != nil {
				t.Fatal(changeErr)
			}
			router := gin.New()
			router.Use(sessions.Sessions("authority-session", cookie.NewStore([]byte("authority-cookie-fixture"))))
			if contextID > 0 {
				router.Use(func(c *gin.Context) { c.Set("id", contextID); c.Next() })
			}
			handled := false
			router.POST("/v1/videos", TokenAuth(), func(c *gin.Context) { handled = true; c.Status(http.StatusOK) })
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			expectedStatus, expectedCode := http.StatusUnauthorized, "invalid_api_key"
			switch name {
			case "expired_parent", "expired_session_token":
				expectedCode = "token_expired"
			case "disabled_parent", "disabled_session_token":
				expectedCode = "token_disabled"
			case "exhausted_parent", "exhausted_session_token":
				expectedStatus, expectedCode = http.StatusTooManyRequests, "key_budget_exceeded"
			}
			var payload map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != expectedStatus || consoleErrorCode(payload) != expectedCode || response.Header().Get("Retry-After") != "" || handled {
				t.Fatalf("confirmed credential state changed contract: status=%d code=%s retry=%q handled=%v body=%s", response.Code, consoleErrorCode(payload), response.Header().Get("Retry-After"), handled, response.Body.String())
			}
		})
	}
}

func TestCookieVersionAuthorityOutagePreservesValidSession(t *testing.T) {
	for _, name := range []string{"user_auth", "optional_user_auth", "relay_session_token"} {
		t.Run(name, func(t *testing.T) {
			fixture := newAuthorityFixture(t)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Use(sessions.Sessions("authority-session", cookie.NewStore([]byte("authority-cookie-fixture"))))
			router.GET("/login", func(c *gin.Context) {
				session := sessions.Default(c)
				for key, value := range map[string]any{"username": fixture.user.Username, "role": fixture.user.Role, "id": fixture.user.Id, "status": fixture.user.Status, "session_version": fixture.user.SessionVersion} {
					session.Set(key, value)
				}
				if err := session.Save(); err != nil {
					t.Fatal(err)
				}
				c.Status(http.StatusOK)
			})
			handled := false
			handler := func(c *gin.Context) { handled = true; c.Status(http.StatusOK) }
			path := "/protected"
			if name == "user_auth" {
				router.GET(path, UserAuth(), handler)
			}
			if name == "optional_user_auth" {
				router.GET(path, TryUserAuth(), handler)
			}
			if name == "relay_session_token" {
				path = "/v1/videos"
				router.GET(path, TokenAuth(), handler)
			}
			login := httptest.NewRecorder()
			router.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
			cookies := login.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("expected one real session cookie: %v", cookies)
			}
			calls := injectAuthoritySQLFailure(t, fixture.db, "users")
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.AddCookie(cookies[0])
			request.Header.Set("X-Lazu-User", strconv.Itoa(fixture.user.Id))
			request.Header.Set("X-Token-Id", strconv.Itoa(fixture.token.Id))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assertAuthorityUnavailable(t, response)
			if handled || *calls != 1 || len(response.Result().Cookies()) != 0 {
				t.Fatalf("outage trusted/cleared cookie: handled=%v calls=%d cookies=%v", handled, *calls, response.Result().Cookies())
			}
			if err := fixture.db.Callback().Query().Remove("test:authority-query"); err != nil {
				t.Fatal(err)
			}
			// The same encrypted cookie must become usable again; authority
			// failure did not silently revoke the user's actual session.
			if name == "relay_session_token" {
				contextRouter := gin.New()
				contextRouter.Use(sessions.Sessions("authority-session", cookie.NewStore([]byte("authority-cookie-fixture"))))
				contextRouter.GET(path, func(c *gin.Context) {
					token, resolved, err := resolveTokenByIDHeader(c)
					if err != nil || !resolved || token.Id != fixture.token.Id {
						t.Fatalf("original cookie did not recover: token=%+v resolved=%v err=%v", token, resolved, err)
					}
					c.Status(http.StatusOK)
				})
				router = contextRouter
			}
			recovered := httptest.NewRecorder()
			router.ServeHTTP(recovered, request)
			if recovered.Code != http.StatusOK {
				t.Fatalf("original cookie refused after SQL recovery: %d %s", recovered.Code, recovered.Body.String())
			}
			// A subsequently confirmed version change still revokes and clears.
			if err := fixture.db.Model(&model.User{}).Where("id = ?", fixture.user.Id).Update("session_version", fixture.user.SessionVersion+1).Error; err != nil {
				t.Fatal(err)
			}
			handled = false
			revoked := httptest.NewRecorder()
			if name == "relay_session_token" {
				// Use the actual TokenAuth mapper again for the revoked cookie.
				router = gin.New()
				router.Use(sessions.Sessions("authority-session", cookie.NewStore([]byte("authority-cookie-fixture"))))
				router.GET(path, TokenAuth(), handler)
			}
			router.ServeHTTP(revoked, request)
			if len(revoked.Result().Cookies()) == 0 {
				t.Fatalf("confirmed revocation did not clear session: %d %s", revoked.Code, revoked.Body.String())
			}
			if name == "optional_user_auth" {
				if revoked.Code != http.StatusOK {
					t.Fatalf("optional stale-session contract changed: %d", revoked.Code)
				}
			} else if revoked.Code != http.StatusUnauthorized || handled {
				t.Fatalf("revoked cookie admitted: status=%d handled=%v body=%s", revoked.Code, handled, revoked.Body.String())
			}
		})
	}
}
