package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// userAPIErrorBody is the console error envelope as a client reads it.
type userAPIErrorBody struct {
	Success bool `json:"success"`
	Error   struct {
		Code    string         `json:"code"`
		Type    string         `json:"type"`
		Lazu    string         `json:"lazu"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// requireUserAPIError asserts a console failure: the real HTTP status, the
// catalog code and an LZ number.
func requireUserAPIError(t *testing.T, w *httptest.ResponseRecorder, status int, code types.ErrorCode) userAPIErrorBody {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	var body userAPIErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v; body=%s", err, w.Body.String())
	}
	if body.Success {
		t.Fatalf("failure answered success=true: %s", w.Body.String())
	}
	if body.Error.Code != string(code) {
		t.Fatalf("error.code = %q, want %q; body=%s", body.Error.Code, code, w.Body.String())
	}
	if body.Error.Lazu == "" {
		t.Fatalf("error.lazu is empty: %s", w.Body.String())
	}
	return body
}

func newUserTestContext(method string, target string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func TestLoginAnswersPasswordLoginDisabled(t *testing.T) {
	previousPasswordLogin := common.PasswordLoginEnabled
	previousBreakGlass := common.OIDCBreakGlassPasswordEnabled
	common.PasswordLoginEnabled = false
	common.OIDCBreakGlassPasswordEnabled = false
	t.Cleanup(func() {
		common.PasswordLoginEnabled = previousPasswordLogin
		common.OIDCBreakGlassPasswordEnabled = previousBreakGlass
	})

	c, w := newUserTestContext(http.MethodPost, "/api/user/login", `{"username":"someone","password":"secret-password"}`)
	Login(c)

	requireUserAPIError(t, w, http.StatusForbidden, types.ErrorCodePasswordLoginDisabled)
}

func TestAdjustUserQuotaRejectsZeroAmount(t *testing.T) {
	c, w := newUserTestContext(http.MethodPost, "/api/user/5/quota/adjust", `{"delta_microusd":0,"reason":"refund"}`)
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	c.Set("role", common.RoleRootUser)

	AdjustUserQuota(c)

	requireUserAPIError(t, w, http.StatusBadRequest, types.ErrorCodeBalanceAdjustmentAmountZero)
}

// seedStandingAdmin keeps an enabled administrator in the table so a test can
// demote or disable another one without tripping the last-admin guard.
func seedStandingAdmin(t *testing.T) model.User {
	t.Helper()
	standing := model.User{Username: "standing-admin", Password: "placeholder-hash", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	if err := model.CoreDB().Create(&standing).Error; err != nil {
		t.Fatalf("seed standing admin: %v", err)
	}
	return standing
}

func TestGetUserRefusesNonAdmin(t *testing.T) {
	setupOIDCTestDB(t)
	peer := model.User{Username: "peer-customer", Password: "placeholder-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if err := model.CoreDB().Create(&peer).Error; err != nil {
		t.Fatalf("seed peer: %v", err)
	}
	c, w := newUserTestContext(http.MethodGet, "/api/user/peer", "")
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(peer.Id)}}
	c.Set("role", common.RoleCommonUser)

	GetUser(c)

	body := requireUserAPIError(t, w, http.StatusForbidden, types.ErrorCodeUserManageForbidden)
	if body.Error.Details["action"] != "view" {
		t.Fatalf("details.action = %v, want view", body.Error.Details["action"])
	}
}

func TestGetUserAnswersUserNotFound(t *testing.T) {
	setupOIDCTestDB(t)
	c, w := newUserTestContext(http.MethodGet, "/api/user/424242", "")
	c.Params = gin.Params{{Key: "id", Value: "424242"}}
	c.Set("role", common.RoleRootUser)

	GetUser(c)

	requireUserAPIError(t, w, http.StatusNotFound, types.ErrorCodeUserNotFound)
}

func TestManageUserProtectsLastAdminFromDemotion(t *testing.T) {
	setupOIDCTestDB(t)
	root := model.User{Username: "root-owner", Password: "placeholder-hash", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	if err := model.CoreDB().Create(&root).Error; err != nil {
		t.Fatalf("seed root: %v", err)
	}
	c, w := newUserTestContext(http.MethodPost, "/api/user/manage", `{"id":"`+strconv.Itoa(root.Id)+`","action":"demote"}`)
	c.Set("role", common.RoleRootUser)

	ManageUser(c)

	body := requireUserAPIError(t, w, http.StatusForbidden, types.ErrorCodeRootUserProtected)
	if body.Error.Details["action"] != "demoted while it is the last administrator" {
		t.Fatalf("details.action = %v, want last-administrator demotion", body.Error.Details["action"])
	}
}

func TestManageUserRefusesSelfDemotion(t *testing.T) {
	setupOIDCTestDB(t)
	self := model.User{Username: "self-admin", Password: "placeholder-hash", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	if err := model.CoreDB().Create(&self).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	seedStandingAdmin(t)
	c, w := newUserTestContext(http.MethodPost, "/api/user/manage", `{"id":"`+strconv.Itoa(self.Id)+`","action":"demote"}`)
	c.Set("role", common.RoleRootUser)
	c.Set("id", self.Id)

	ManageUser(c)

	body := requireUserAPIError(t, w, http.StatusForbidden, types.ErrorCodeRootUserProtected)
	if body.Error.Details["action"] != "demoted by itself" {
		t.Fatalf("details.action = %v, want self demotion", body.Error.Details["action"])
	}
}

func TestManageUserPromotesAndDemotesBetweenTheTwoRoles(t *testing.T) {
	setupOIDCTestDB(t)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedisEnabled })
	actor := seedStandingAdmin(t)
	target := model.User{Username: "toggle-target", Password: "placeholder-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if err := model.CoreDB().Create(&target).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	for _, step := range []struct {
		action string
		want   int
	}{{"promote", common.RoleRootUser}, {"demote", common.RoleCommonUser}} {
		c, w := newUserTestContext(http.MethodPost, "/api/user/manage", `{"id":"`+strconv.Itoa(target.Id)+`","action":"`+step.action+`"}`)
		c.Set("role", common.RoleRootUser)
		c.Set("id", actor.Id)
		ManageUser(c)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", step.action, w.Code, w.Body.String())
		}
		var stored model.User
		if err := model.CoreDB().First(&stored, target.Id).Error; err != nil {
			t.Fatalf("reload: %v", err)
		}
		if stored.Role != step.want {
			t.Fatalf("%s: role=%d want %d", step.action, stored.Role, step.want)
		}
	}
}

func TestUpdateSelfRejectsReservedUsername(t *testing.T) {
	setupOIDCTestDB(t)
	owner := seedUsernameUser(t, "plain-owner")
	c, w := newUserTestContext(http.MethodPut, "/api/user/self", `{"username":"admin"}`)
	c.Set("id", owner.Id)

	UpdateSelf(c)

	body := requireUserAPIError(t, w, http.StatusConflict, types.ErrorCodeUsernameUnavailable)
	if body.Error.Details["reason"] != "reserved" {
		t.Fatalf("details.reason = %v, want reserved", body.Error.Details["reason"])
	}
}

func TestUsernameVerdictCarriesItsCode(t *testing.T) {
	cases := map[string]types.ErrorCode{
		"available":      "",
		"unchanged":      "",
		"too_short":      types.ErrorCodeUsernameInvalid,
		"too_long":       types.ErrorCodeUsernameInvalid,
		"invalid_format": types.ErrorCodeUsernameInvalid,
		"reserved":       types.ErrorCodeUsernameUnavailable,
		"taken":          types.ErrorCodeUsernameUnavailable,
		"cooldown":       types.ErrorCodeUsernameChangeCooldown,
	}
	for status, want := range cases {
		verdict := usernameVerdictOf(status, "candidate")
		if verdict.Code != want {
			t.Fatalf("%s: code = %q, want %q", status, verdict.Code, want)
		}
		if want != "" && types.ErrCode(verdict.err()) != want {
			t.Fatalf("%s: err() code = %q, want %q", status, types.ErrCode(verdict.err()), want)
		}
	}
}

func TestPreviewAuthEmailUnknownTemplate(t *testing.T) {
	c, w := newUserTestContext(http.MethodGet, "/api/auth/email-preview/nope", "")
	c.Params = gin.Params{{Key: "template", Value: "nope"}}

	PreviewAuthEmail(c)

	requireUserAPIError(t, w, http.StatusNotFound, types.ErrorCodeResourceNotFound)
}

func TestStartEmailAuthAnswersDisabled(t *testing.T) {
	previous := common.EmailCodeAuthEnabled
	common.EmailCodeAuthEnabled = false
	t.Cleanup(func() { common.EmailCodeAuthEnabled = previous })

	c, w := newUserTestContext(http.MethodPost, "/api/auth/email/start", `{"email":"person@example.com"}`)
	StartEmailAuth(c)

	requireUserAPIError(t, w, http.StatusForbidden, types.ErrorCodeEmailCodeAuthDisabled)
}

func TestVerifyEmailAuthRejectsWrongCode(t *testing.T) {
	previous := common.EmailCodeAuthEnabled
	previousRedis := common.RedisEnabled
	common.EmailCodeAuthEnabled = true
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.EmailCodeAuthEnabled = previous
		common.RedisEnabled = previousRedis
		common.ClearVerificationFailures("wrong-code@example.com", common.EmailAuthPurpose)
	})

	c, w := newUserTestContext(http.MethodPost, "/api/auth/email/verify", `{"email":"wrong-code@example.com","code":"000000"}`)
	VerifyEmailAuth(c)

	requireUserAPIError(t, w, http.StatusBadRequest, types.ErrorCodeVerificationCodeInvalid)
}

func TestGetSelfOnboardingAnswersUserNotFound(t *testing.T) {
	setupOIDCTestDB(t)
	c, w := newUserTestContext(http.MethodGet, "/api/user/self/onboarding", "")
	c.Set("id", 987654)

	GetSelfOnboarding(c)

	requireUserAPIError(t, w, http.StatusNotFound, types.ErrorCodeUserNotFound)
}

func TestOIDCBootstrapWithoutSessionAnswersSessionRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))))
	engine.GET("/bootstrap", OIDCBootstrap)

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/bootstrap", nil))

	requireUserAPIError(t, recorder, http.StatusUnauthorized, types.ErrorCodeSessionRequired)
}

func TestOAuthRedirectCodeHidesUnexpectedFailures(t *testing.T) {
	if got := oauthRedirectCode(errOAuthEmailConflict); got != types.ErrorCodeOAuthEmailConflict {
		t.Fatalf("conflict redirect code = %q", got)
	}
	if got := oauthRedirectCode(errUserTestPlain("database is down")); got != types.ErrorCodeOAuthLoginFailed {
		t.Fatalf("unexpected failure redirect code = %q", got)
	}
}

type errUserTestPlain string

func (e errUserTestPlain) Error() string { return string(e) }

func TestAdminUserMutationsRejectMalformedBodies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		handler   gin.HandlerFunc
		wrongType string
	}{
		{"quota/adjust", AdjustUserQuota, `{"delta_microusd":"not-an-amount","reason":"refund"}`},
		{"has_topped_up", SetUserHasToppedUp, `{"has_topped_up":"not-a-boolean"}`},
	} {
		for _, payload := range []string{"", "{", tc.wrongType} {
			t.Run(tc.name+"/"+payload, func(t *testing.T) {
				c, w := newUserTestContext(http.MethodPost, "/api/user/5/"+tc.name, payload)
				c.Params = gin.Params{{Key: "id", Value: "5"}}
				c.Set("role", common.RoleRootUser)
				tc.handler(c)
				body := requireUserAPIError(t, w, http.StatusBadRequest, types.ErrorCodeInvalidRequest)
				if body.Error.Details["field"] != "body" {
					t.Fatalf("missing field: %s", w.Body)
				}
			})
		}
	}
}
