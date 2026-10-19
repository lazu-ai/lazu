package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func performAuthRequest(t *testing.T, middleware gin.HandlerFunc, sessionValues map[string]any, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	return performAuthRequestURL(t, middleware, sessionValues, headers, "/protected")
}

func performAuthRequestURL(t *testing.T, middleware gin.HandlerFunc, sessionValues map[string]any, headers map[string]string, requestURL string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test-session", store))

	if sessionValues != nil {
		r.Use(func(c *gin.Context) {
			session := sessions.Default(c)
			for k, v := range sessionValues {
				session.Set(k, v)
			}
			_ = session.Save()
			c.Next()
		})
	}

	r.GET("/protected", middleware, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"id":               c.GetInt("id"),
			"token_id":         c.GetInt("token_id"),
			"use_access_token": c.GetBool("use_access_token"),
		})
	})

	req := httptest.NewRequest(http.MethodGet, requestURL, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var payload map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	return w, payload
}

func TestUserOrTokenAuthAcceptsModelAPIKey(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := &model.User{
		Id: 761, Username: "health-api-key", Password: "password123",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := &model.Token{
		Id: 762, UserId: user.Id, Key: "healthroutekey",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100,
		Group: model.ChannelLaneAgent,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	w, payload := performAuthRequest(t, UserOrTokenAuth(), nil, map[string]string{
		"Authorization": "Bearer sk-" + token.Key,
	})
	if w.Code != http.StatusOK || payload["id"] != float64(user.Id) || payload["token_id"] != float64(token.Id) {
		t.Fatalf("API key was not accepted: status=%d payload=%v", w.Code, payload)
	}
}

func TestUserOrTokenAuthNormalizesAlternateAPIKeyCarriers(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := &model.User{
		Id: 764, Username: "health-alt-api-key", Password: "password123",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := &model.Token{
		Id: 765, UserId: user.Id, Key: "healthalternatekey",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100,
		Group: model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	apiKey := "sk-" + token.Key
	tests := []struct {
		name       string
		headers    map[string]string
		requestURL string
	}{
		{name: "anthropic header", headers: map[string]string{"x-api-key": apiKey}, requestURL: "/protected"},
		{name: "gemini header", headers: map[string]string{"x-goog-api-key": apiKey}, requestURL: "/protected"},
		{name: "gemini query", headers: map[string]string{}, requestURL: "/protected?key=" + apiKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w, payload := performAuthRequestURL(t, UserOrTokenAuth(), nil, test.headers, test.requestURL)
			if w.Code != http.StatusOK || payload["id"] != float64(user.Id) || payload["token_id"] != float64(token.Id) {
				t.Fatalf("alternate API key was not accepted: status=%d payload=%v", w.Code, payload)
			}
		})
	}
}

func TestUserOrTokenAuthKeepsConsoleSessionAuthoritative(t *testing.T) {
	w, payload := performAuthRequest(t, UserOrTokenAuth(), map[string]any{
		"username": "session-user", "role": common.RoleCommonUser, "id": 763,
		"status": common.UserStatusEnabled,
	}, map[string]string{
		"Authorization": "Bearer console-access-token",
		"X-Lazu-User":   "763",
	})
	if w.Code != http.StatusOK || payload["id"] != float64(763) || payload["token_id"] != float64(0) {
		t.Fatalf("session was displaced by Authorization header: status=%d payload=%v", w.Code, payload)
	}
}

func newGinTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, w
}

func performSessionHelperRequest(t *testing.T, path string, sessionValues map[string]any, headers map[string]string, handler gin.HandlerFunc) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test-session", store))

	if sessionValues != nil {
		r.Use(func(c *gin.Context) {
			session := sessions.Default(c)
			for k, v := range sessionValues {
				session.Set(k, v)
			}
			_ = session.Save()
			c.Next()
		})
	}

	r.GET("/*any", handler)

	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	payload := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	return w, payload
}

func TestUserAuth_RejectsWithoutSessionOrAccessToken(t *testing.T) {
	w, payload := performAuthRequest(t, UserAuth(), nil, nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
	if payload["success"] != false {
		t.Fatalf("expected success=false, got %v", payload["success"])
	}
}

func TestUserAuth_RejectsWhenMissingLazuUserHeader(t *testing.T) {
	w, payload := performAuthRequest(t, UserAuth(), map[string]any{
		"username": "alice",
		"role":     common.RoleCommonUser,
		"id":       1,
		"status":   common.UserStatusEnabled,
	}, nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
	if payload["success"] != false {
		t.Fatalf("expected success=false, got %v", payload["success"])
	}
}

func TestUserAuth_RejectsMismatchedLazuUserHeader(t *testing.T) {
	w, payload := performAuthRequest(t, UserAuth(), map[string]any{
		"username": "alice",
		"role":     common.RoleCommonUser,
		"id":       1,
		"status":   common.UserStatusEnabled,
	}, map[string]string{
		"X-Lazu-User": "2",
	})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
	if payload["success"] != false {
		t.Fatalf("expected success=false, got %v", payload["success"])
	}
}

func TestUserAuth_RejectsDisabledUser(t *testing.T) {
	w, payload := performAuthRequest(t, UserAuth(), map[string]any{
		"username": "alice",
		"role":     common.RoleCommonUser,
		"id":       1,
		"status":   common.UserStatusDisabled,
	}, map[string]string{
		"X-Lazu-User": "1",
	})

	if w.Code != http.StatusForbidden || consoleErrorCode(payload) != "user_banned" {
		t.Fatalf("expected 403 user_banned, got %d %v", w.Code, payload)
	}
	if payload["success"] != false {
		t.Fatalf("expected success=false, got %v", payload["success"])
	}
}

func TestAdminAuth_RejectsInsufficientRole(t *testing.T) {
	w, payload := performAuthRequest(t, RootAuth(), map[string]any{
		"username": "alice",
		"role":     common.RoleCommonUser,
		"id":       1,
		"status":   common.UserStatusEnabled,
	}, map[string]string{
		"X-Lazu-User": "1",
	})

	if w.Code != http.StatusForbidden || consoleErrorCode(payload) != "access_denied" {
		t.Fatalf("expected 403 access_denied, got %d %v", w.Code, payload)
	}
	if payload["success"] != false {
		t.Fatalf("expected success=false, got %v", payload["success"])
	}
}

func TestPlatformOperationsBoundary_RejectsCustomerWorkspaceRole(t *testing.T) {
	w, payload := performAuthRequest(t, RootAuth(), map[string]any{
		"username": "workspace-member",
		"role":     common.RoleCommonUser,
		"id":       42,
		"status":   common.UserStatusEnabled,
	}, map[string]string{
		"X-Lazu-User": "42",
	})

	if w.Code != http.StatusForbidden || payload["success"] != false {
		t.Fatalf("customer Workspace identity reached Platform Operations: status=%d payload=%v", w.Code, payload)
	}
	if _, reachedProtectedHandler := payload["id"]; reachedProtectedHandler {
		t.Fatalf("customer Workspace identity reached the protected Platform handler: %v", payload)
	}
}

func TestUserAuth_AllowsValidSessionAndSetsContext(t *testing.T) {
	w, payload := performAuthRequest(t, UserAuth(), map[string]any{
		"username": "alice",
		"role":     common.RoleCommonUser,
		"id":       7,
		"status":   common.UserStatusEnabled,
	}, map[string]string{
		"X-Lazu-User": "7",
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if payload["id"] != float64(7) {
		t.Fatalf("expected id=7, got %v", payload["id"])
	}
	if payload["use_access_token"] != false {
		t.Fatalf("expected use_access_token=false, got %v", payload["use_access_token"])
	}
}

func TestUserAuth_RejectsSessionIssuedBeforePasswordChange(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := model.User{
		Id:             73,
		Username:       "versioned-session-user",
		Password:       "placeholder-hashed-password-value",
		SessionVersion: 2,
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed versioned user: %v", err)
	}

	sessionValues := map[string]any{
		"username":        user.Username,
		"role":            user.Role,
		"id":              user.Id,
		"status":          user.Status,
		"session_version": 1,
	}
	w, payload := performAuthRequest(t, UserAuth(), sessionValues, map[string]string{
		"X-Lazu-User": "73",
	})
	if w.Code != http.StatusUnauthorized || consoleErrorCode(payload) != "session_expired" {
		t.Fatalf("stale session was accepted: status=%d payload=%v", w.Code, payload)
	}

	sessionValues["session_version"] = 2
	w, payload = performAuthRequest(t, UserAuth(), sessionValues, map[string]string{
		"X-Lazu-User": "73",
	})
	if w.Code != http.StatusOK || payload["id"] != float64(73) {
		t.Fatalf("current session was rejected: status=%d payload=%v", w.Code, payload)
	}
}

func TestUserAuthRequiresExplicitValidSessionVersion(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := model.User{
		Id:             75,
		Username:       "zero-version-user",
		Password:       "placeholder-hashed-password-value",
		SessionVersion: 0,
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed zero-version user: %v", err)
	}
	base := map[string]any{
		"username": user.Username,
		"role":     user.Role,
		"id":       user.Id,
		"status":   user.Status,
	}

	for name, invalidVersion := range map[string]any{
		"missing":    nil,
		"string":     "0",
		"fractional": 0.0,
		"negative":   -1,
	} {
		t.Run(name, func(t *testing.T) {
			sessionValues := make(map[string]any, len(base)+1)
			for key, value := range base {
				sessionValues[key] = value
			}
			if invalidVersion != nil {
				sessionValues["session_version"] = invalidVersion
			}
			w, payload := performAuthRequest(t, UserAuth(), sessionValues, map[string]string{"X-Lazu-User": "75"})
			if w.Code != http.StatusUnauthorized || consoleErrorCode(payload) != "session_expired" {
				t.Fatalf("invalid session version was accepted: status=%d payload=%v", w.Code, payload)
			}
		})
	}

	base["session_version"] = 0
	w, payload := performAuthRequest(t, UserAuth(), base, map[string]string{"X-Lazu-User": "75"})
	if w.Code != http.StatusOK || payload["id"] != float64(75) {
		t.Fatalf("explicit version zero was rejected: status=%d payload=%v", w.Code, payload)
	}
}

func TestTryUserAuthDoesNotTrustAStaleSession(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := model.User{
		Id:             74,
		Username:       "optional-versioned-user",
		Password:       "placeholder-hashed-password-value",
		SessionVersion: 3,
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(&user).Error; err != nil {
		t.Fatalf("seed optional-auth user: %v", err)
	}
	sessionValues := map[string]any{
		"id":              user.Id,
		"session_version": 2,
	}
	w, payload := performAuthRequest(t, TryUserAuth(), sessionValues, nil)
	if w.Code != http.StatusOK || payload["id"] != float64(0) {
		t.Fatalf("stale optional session was trusted: status=%d payload=%v", w.Code, payload)
	}
	sessionValues["session_version"] = 3
	w, payload = performAuthRequest(t, TryUserAuth(), sessionValues, nil)
	if w.Code != http.StatusOK || payload["id"] != float64(74) {
		t.Fatalf("current optional session was rejected: status=%d payload=%v", w.Code, payload)
	}
}

func TestSetupContextForToken_RejectsNilToken(t *testing.T) {
	c, _ := newGinTestContext()
	if err := SetupContextForToken(c, nil); err == nil {
		t.Fatal("expected nil token to return error")
	}
}

func TestRejectEphemeralCredentialOutsideRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDistributorTestDB(t)
	defer cleanup()

	user := &model.User{
		Id:       4101,
		Username: "ephemeral_non_relay_user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	if err := model.CoreDB().Create(user).Error; err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	token := &model.Token{
		Id:          5101,
		UserId:      user.Id,
		Key:         "ephemeralnonrelaytoken",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 100,
		Group:       model.ChannelLaneDirect,
	}
	if err := token.Insert(); err != nil {
		t.Fatalf("failed to insert token: %v", err)
	}
	issued, err := model.IssueEphemeralCredential(model.IssueEphemeralCredentialInput{
		UserID:     user.Id,
		TokenID:    token.Id,
		SessionID:  "sess-non-relay",
		Models:     []string{"gpt-scoped"},
		Providers:  []string{"openai"},
		TTLSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to issue ephemeral credential: %v", err)
	}

	engine := gin.New()
	engine.GET("/v1/files", TokenAuth(), RejectEphemeralCredentialOutsideRelay(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	ephemeralReq := httptest.NewRequest(http.MethodGet, "/v1/files", nil)
	ephemeralReq.Header.Set("Authorization", "Bearer "+issued.Credential)
	ephemeralResp := httptest.NewRecorder()
	engine.ServeHTTP(ephemeralResp, ephemeralReq)
	if ephemeralResp.Code != http.StatusForbidden {
		t.Fatalf("expected ephemeral credential to be rejected, got %d body=%s", ephemeralResp.Code, ephemeralResp.Body.String())
	}
	var deniedAudit model.EphemeralCredentialAudit
	if err := model.CoreDB().Where("event_type = ?", model.EphemeralCredentialAuditDenied).First(&deniedAudit).Error; err != nil {
		t.Fatalf("expected denied audit for non-relay ephemeral use: %v", err)
	}

	tokenReq := httptest.NewRequest(http.MethodGet, "/v1/files", nil)
	tokenReq.Header.Set("Authorization", "Bearer "+token.Key)
	tokenResp := httptest.NewRecorder()
	engine.ServeHTTP(tokenResp, tokenReq)
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("expected ordinary token to pass, got %d body=%s", tokenResp.Code, tokenResp.Body.String())
	}
}

func TestSetupContextForToken_SetsContextFields(t *testing.T) {
	c, _ := newGinTestContext()

	token := &model.Token{
		Id:                  3,
		UserId:              9,
		Key:                 "abc123",
		Name:                "demo-token",
		Product:             "search-api",
		Metadata:            model.JSONValue([]byte(`{"env":"prod"}`)),
		UnlimitedQuota:      false,
		RemainQuota:         88,
		RateLimitsEnabled:   true,
		RPMLimit:            12,
		TPMLimit:            3456,
		ModelLimitsEnabled:  true,
		ModelLimits:         `{"gpt-4o":"enabled"}`,
		Group:               "vip",
		VendorLimitsEnabled: true,
		VendorLimits:        model.IntSlice{1, 2},
	}

	if err := SetupContextForToken(c, token); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if c.GetInt("id") != 9 {
		t.Fatalf("expected id=9, got %d", c.GetInt("id"))
	}
	if c.GetInt("token_id") != 3 {
		t.Fatalf("expected token_id=3, got %d", c.GetInt("token_id"))
	}
	if c.GetString("token_name") != "demo-token" {
		t.Fatalf("expected token_name=demo-token, got %s", c.GetString("token_name"))
	}
	if product := common.GetContextKeyString(c, constant.ContextKeyTokenProduct); product != "search-api" {
		t.Fatalf("expected %s=search-api, got %q", constant.ContextKeyTokenProduct, product)
	}
	if metadata := common.GetContextKeyString(c, constant.ContextKeyTokenMetadata); metadata != `{"env":"prod"}` {
		t.Fatalf("expected %s to round-trip, got %q", constant.ContextKeyTokenMetadata, metadata)
	}
	if c.GetBool("token_model_limit_enabled") != true {
		t.Fatalf("expected token_model_limit_enabled=true")
	}
	if enabled := common.GetContextKeyBool(c, constant.ContextKeyTokenRateLimitsEnabled); !enabled {
		t.Fatalf("expected %s=true", constant.ContextKeyTokenRateLimitsEnabled)
	}
	if rpm := common.GetContextKeyInt(c, constant.ContextKeyTokenRPMLimit); rpm != 12 {
		t.Fatalf("expected %s=12, got %d", constant.ContextKeyTokenRPMLimit, rpm)
	}
	if tpm := common.GetContextKeyInt(c, constant.ContextKeyTokenTPMLimit); tpm != 3456 {
		t.Fatalf("expected %s=3456, got %d", constant.ContextKeyTokenTPMLimit, tpm)
	}

	if enabled := common.GetContextKeyBool(c, constant.ContextKeyTokenVendorLimitEnabled); !enabled {
		t.Fatalf("expected %s=true", constant.ContextKeyTokenVendorLimitEnabled)
	}
	if _, ok := common.GetContextKey(c, constant.ContextKeyTokenVendorLimits); !ok {
		t.Fatalf("expected %s to be set", constant.ContextKeyTokenVendorLimits)
	}
}

func TestSetupContextForToken_RejectsSpecificChannelForNonAdmin(t *testing.T) {
	c, w := newGinTestContext()

	token := &model.Token{
		UserId: 0, // userId=0 short-circuits IsAdmin check to false without DB dependency
	}

	err := SetupContextForToken(c, token, "token", "channel-1")
	if err == nil {
		t.Fatal("expected non-admin specific channel to return error")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", w.Code)
	}
}

func TestParseIntFromAny(t *testing.T) {
	if got := common.ParseIntFromAny(12); got != 12 {
		t.Fatalf("expected 12, got %d", got)
	}
	if got := common.ParseIntFromAny("34"); got != 34 {
		t.Fatalf("expected 34, got %d", got)
	}
	if got := common.ParseIntFromAny("not-int"); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestResolveSessionUserID_FromContext(t *testing.T) {
	c, _ := newGinTestContext()
	c.Set("id", 42)
	if got, err := resolveSessionUserID(c); err != nil || got != 42 {
		t.Fatalf("expected 42, got %d err=%v", got, err)
	}
}

func TestResolveSessionUserID_FromSession(t *testing.T) {
	_, payload := performSessionHelperRequest(
		t,
		"/test",
		map[string]any{"id": 77},
		nil,
		func(c *gin.Context) {
			userID, err := resolveSessionUserID(c)
			if err != nil {
				t.Fatal(err)
			}
			c.JSON(http.StatusOK, gin.H{"id": userID})
		},
	)
	if payload["id"] != float64(77) {
		t.Fatalf("expected id=77 from session, got %v", payload["id"])
	}
}

func TestResolveTokenByIDHeader_NotHandledForUnsupportedPath(t *testing.T) {
	c, _ := newGinTestContext()
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Token-Id", "1")
	token, handled, err := resolveTokenByIDHeader(c)
	if handled {
		t.Fatalf("expected handled=false, got true err=%v token=%v", err, token)
	}
	if err != nil {
		t.Fatalf("expected err=nil, got %v", err)
	}
	if token != nil {
		t.Fatalf("expected token=nil, got %+v", token)
	}
}

func TestResolveTokenByIDHeader_InvalidTokenIDHeader(t *testing.T) {
	_, payload := performSessionHelperRequest(
		t,
		"/v1/images/generations",
		map[string]any{"id": 1},
		map[string]string{"X-Token-Id": "abc"},
		func(c *gin.Context) {
			_, handled, err := resolveTokenByIDHeader(c)
			message := ""
			if err != nil {
				message = err.Error()
			}
			c.JSON(http.StatusOK, gin.H{"handled": handled, "error": message})
		},
	)
	if payload["handled"] != true {
		t.Fatalf("expected handled=true, got %v", payload["handled"])
	}
	if payload["error"] == "" {
		t.Fatal("expected error for invalid token id header")
	}
}

func TestResolveTokenByIDHeader_RequiresAuthenticatedSession(t *testing.T) {
	_, payload := performSessionHelperRequest(
		t,
		"/v1/images/generations",
		nil,
		map[string]string{"X-Token-Id": "1"},
		func(c *gin.Context) {
			_, handled, err := resolveTokenByIDHeader(c)
			message := ""
			if err != nil {
				message = err.Error()
			}
			c.JSON(http.StatusOK, gin.H{"handled": handled, "error": message})
		},
	)
	if payload["handled"] != true {
		t.Fatalf("expected handled=true, got %v", payload["handled"])
	}
	if payload["error"] == "" {
		t.Fatal("expected non-empty error for missing session user")
	}
}

func TestResolveTokenByIDHeaderRequiresTrustedRelayForInternalToken(t *testing.T) {
	previousDB := model.CoreDB()
	db, err := gorm.Open(gormsqlite.Open("file:auth_internal_token?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open auth test database: %v", err)
	}
	model.SetCoreDB(db)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		model.SetCoreDB(previousDB)
	})
	if err := db.AutoMigrate(&model.Token{}); err != nil {
		t.Fatalf("migrate token table: %v", err)
	}
	internalToken := &model.Token{
		Id:             41,
		UserId:         7,
		Name:           authorityHiddenTokenKind.Name,
		Product:        authorityHiddenTokenKind.Product,
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	if err := db.Create(internalToken).Error; err != nil {
		t.Fatalf("create internal token: %v", err)
	}

	context, _ := newGinTestContext()
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	context.Request.Header.Set("X-Token-Id", "41")
	context.Set("id", 7)
	if _, handled, err := resolveTokenByIDHeader(context); !handled || err == nil {
		t.Fatalf("untrusted internal token header should be rejected: handled=%v err=%v", handled, err)
	}

	context.Set(internalProxyVerifiedKey, true)
	token, handled, err := resolveTokenByIDHeader(context)
	if !handled || err != nil || token == nil || !token.IsInternal() {
		t.Fatalf("trusted internal relay should resolve its token: handled=%v token=%+v err=%v", handled, token, err)
	}
}

func TestSupportsTokenIDHeader(t *testing.T) {
	testCases := []struct {
		path string
		want bool
	}{
		{path: "/v1/images/generations", want: true},
		{path: "/v1/images/generations/abc", want: true},
		{path: "/v1/videos", want: true},
		{path: "/v1/videos/abc", want: true},
		{path: "/v1/chat/completions", want: false},
		{path: "/api/token/options", want: false},
	}
	for _, tc := range testCases {
		if got := supportsTokenIDHeader(tc.path); got != tc.want {
			t.Fatalf("supportsTokenIDHeader(%q)=%v, want %v", tc.path, got, tc.want)
		}
	}
}

// consoleErrorCode reads error.code from a console error envelope.
func consoleErrorCode(payload map[string]any) string {
	body, _ := payload["error"].(map[string]any)
	code, _ := body["code"].(string)
	return code
}

func TestTokenAuthRefusesRegisteredAddressForAccountsNotOpened(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	t.Cleanup(func() { model.ApplyAccessAddressesForTest("") })

	seed := func(userID, tokenID int, key string) {
		user := &model.User{Id: userID, Username: key, Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
		if err := model.CoreDB().Create(user).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
		token := &model.Token{Id: tokenID, UserId: userID, Key: key, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
		if err := token.Insert(); err != nil {
			t.Fatalf("seed token: %v", err)
		}
	}
	seed(771, 772, "openedaddresskey")
	seed(781, 782, "closedaddresskey")
	model.ApplyAccessAddressesForTest(`[{"url":"https://direct.example.com","kind":"cn_direct","user_ids":[771]}]`)

	call := func(host, key string) int {
		w, _ := performAuthRequestURL(t, TokenAuth(), nil, map[string]string{"Authorization": "Bearer sk-" + key}, "http://"+host+"/protected")
		return w.Code
	}
	if code := call("direct.example.com", "openedaddresskey"); code != http.StatusOK {
		t.Fatalf("opened account refused on its address: %d", code)
	}
	if code := call("direct.example.com", "closedaddresskey"); code != http.StatusForbidden {
		t.Fatalf("account not opened was let through: %d", code)
	}
	if code := call("api.example.com", "closedaddresskey"); code != http.StatusOK {
		t.Fatalf("default address refused: %d", code)
	}
}
