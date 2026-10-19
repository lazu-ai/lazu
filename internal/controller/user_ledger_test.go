package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"gorm.io/gorm"
)

func setupUserLedgerControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.CoreDB()
	previousLogDB := model.ConfiguredTelemetryDB()

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(gormsqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	model.SetCoreDB(db)
	model.SetTelemetryDB(db)
	t.Cleanup(func() {
		model.SetCoreDB(previousDB)
		model.SetTelemetryDB(previousLogDB)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.TopUp{}, &model.UserIdentity{},
		&model.MediaJob{}, &model.SettlementFailure{}, &model.WalletLedger{}, &model.Log{}, &model.AdminAuditLog{}, &model.SignupAttribution{}, &model.LedgerLegacySettlementWaiver{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newAdminRequest(role int, id int, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("role", role) })
	engine.GET("/user/:id/x", handler)
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/user/"+strconv.Itoa(id)+"/x", nil)
	engine.ServeHTTP(recorder, req)
	return recorder
}

func TestGetUserTokensProjectsSafeFieldsOnly(t *testing.T) {
	db := setupUserLedgerControllerDB(t)
	user := model.User{Username: "cust", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	allow := "1.2.3.4"
	token := model.Token{
		UserId: user.Id, Name: "prod", Key: "super-secret-key",
		KeyPrefix: "sk-abc", KeySuffix: "wxyz", Status: 1,
		RemainQuota: 1000, UnlimitedQuota: false, RPMLimit: 30, TPMLimit: 9000,
		ModelLimits: "gpt-4o", AllowIps: &allow, LastUsedAt: 42, UsedQuota: 7,
		Group: "direct", CreatedTime: 10, ExpiredTime: -1,
	}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	recorder := newAdminRequest(common.RoleRootUser, user.Id, GetUserTokens)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "super-secret-key") || strings.Contains(body, "key_digest") {
		t.Fatalf("token listing leaked sensitive material: %s", body)
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    struct {
			Total int `json:"total"`
			Items []struct {
				ID          string `json:"id"`
				KeyPrefix   string `json:"key_prefix"`
				KeySuffix   string `json:"key_suffix"`
				RPMLimit    int    `json:"rpm_limit"`
				ModelLimits string `json:"model_limits"`
				AllowIps    string `json:"allow_ips"`
				ExpiredTime int64  `json:"expired_time"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !parsed.Success || parsed.Data.Total != 1 || len(parsed.Data.Items) != 1 {
		t.Fatalf("unexpected payload: %s", body)
	}
	item := parsed.Data.Items[0]
	if item.ID == "" || item.KeyPrefix == "" || item.KeySuffix == "" {
		t.Fatalf("expected masked key fragments to be populated: %#v", item)
	}
	if item.RPMLimit != 30 || item.ModelLimits != "gpt-4o" || item.AllowIps != "1.2.3.4" || item.ExpiredTime != -1 {
		t.Fatalf("unexpected projection: %#v", item)
	}
}

func TestUserAccountEndpointsEnforceRoleGuard(t *testing.T) {
	db := setupUserLedgerControllerDB(t)
	peer := model.User{Username: "peer-admin", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatalf("seed peer: %v", err)
	}

	// A normal user may not inspect another account.
	for _, handler := range []gin.HandlerFunc{GetUserAccountSummary, GetUserWalletLedgerEvents, GetUserTokens, GetUserAccountDashboard} {
		recorder := newAdminRequest(common.RoleCommonUser, peer.Id, handler)
		var parsed struct {
			Success bool `json:"success"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &parsed)
		if parsed.Success {
			t.Fatalf("expected guard to reject same-level access, body=%s", recorder.Body.String())
		}
	}

	// An administrator may inspect another administrator.
	recorder := newAdminRequest(common.RoleRootUser, peer.Id, GetUserAccountSummary)
	var parsed struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !parsed.Success {
		t.Fatalf("root should be allowed, body=%s", recorder.Body.String())
	}
}

func TestGetUserAccountSummaryShape(t *testing.T) {
	db := setupUserLedgerControllerDB(t)
	previousEnabled := common.EmailCodeAuthEnabled
	previousPasswordEnabled := common.PasswordLoginEnabled
	previousResend := common.AppConfig.Resend.APIKey
	common.EmailCodeAuthEnabled = true
	common.PasswordLoginEnabled = true
	common.AppConfig.Resend.APIKey = "test-resend-key"
	t.Cleanup(func() {
		common.EmailCodeAuthEnabled = previousEnabled
		common.PasswordLoginEnabled = previousPasswordEnabled
		common.AppConfig.Resend.APIKey = previousResend
	})
	user := model.User{
		Username: "detail", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "detail@example.com", Quota: 12345, UsedQuota: 999,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	recorder := newAdminRequest(common.RoleRootUser, user.Id, GetUserAccountSummary)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    struct {
			ID         string `json:"id"`
			InternalID int    `json:"internal_id"`
			Quota      int64  `json:"quota"`
			Username   string `json:"username"`
			RateLimit  struct {
				RPM         int `json:"rpm"`
				TPM         int `json:"tpm"`
				RPMOverride int `json:"rpm_override"`
				TPMOverride int `json:"tpm_override"`
			} `json:"rate_limit"`
			PricePlan *struct {
				ID int `json:"id"`
			} `json:"price_plan"`
			MediaJobs struct {
				Total  int64 `json:"total"`
				Failed int64 `json:"failed"`
			} `json:"media_jobs"`
			SettlementFailures struct {
				Open int64 `json:"open"`
			} `json:"settlement_failures"`
			LoginProviders []string `json:"login_providers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v (%s)", err, recorder.Body.String())
	}
	if !parsed.Success {
		t.Fatalf("expected success, body=%s", recorder.Body.String())
	}
	if parsed.Data.ID != user.PublicID || parsed.Data.InternalID != user.Id || parsed.Data.Quota != 12345 || parsed.Data.Username != "detail" {
		t.Fatalf("unexpected identity fields: %#v", parsed.Data)
	}
	// The account has never topped up, so it runs at the unpaid ceiling.
	if limit := parsed.Data.RateLimit; limit.RPM != setting.RateLimitUnpaidRPM || limit.TPM != setting.RateLimitUnpaidTPM ||
		limit.RPMOverride != 0 || limit.TPMOverride != 0 {
		t.Fatalf("unpaid account should display the unpaid ceiling, got %#v", limit)
	}
	if parsed.Data.PricePlan != nil {
		t.Fatalf("account without a plan should report no price plan, got %#v", parsed.Data.PricePlan)
	}
	if len(parsed.Data.LoginProviders) != 2 || parsed.Data.LoginProviders[0] != "email" || parsed.Data.LoginProviders[1] != "password" {
		t.Fatalf("every usable login method should be surfaced, got %#v", parsed.Data.LoginProviders)
	}
}

func TestLoginProvidersForUserIncludesCompletedPasswordLogin(t *testing.T) {
	previousPasswordEnabled := common.PasswordLoginEnabled
	previousGoogleID, previousGoogleSecret := common.GoogleOAuthClientID, common.GoogleOAuthClientSecret
	common.PasswordLoginEnabled = true
	common.GoogleOAuthClientID, common.GoogleOAuthClientSecret = "client", "secret"
	t.Cleanup(func() {
		common.PasswordLoginEnabled = previousPasswordEnabled
		common.GoogleOAuthClientID, common.GoogleOAuthClientSecret = previousGoogleID, previousGoogleSecret
	})

	providers := loginProvidersForUser([]string{"google"}, "", false)
	if len(providers) != 2 || providers[0] != "google" || providers[1] != "password" {
		t.Fatalf("mixed login methods=%#v, want google and password", providers)
	}
	needsPassword := loginProvidersForUser([]string{"google"}, "", true)
	if len(needsPassword) != 1 || needsPassword[0] != "google" {
		t.Fatalf("unfinished password onboarding must not expose password login: %#v", needsPassword)
	}
	common.GoogleOAuthClientSecret = ""
	unavailable := loginProvidersForUser([]string{"google"}, "", false)
	if len(unavailable) != 1 || unavailable[0] != "password" {
		t.Fatalf("unavailable historical provider should be omitted: %#v", unavailable)
	}
}

func TestLookupCustomerAccountIsExactAndCustomerOnly(t *testing.T) {
	db := setupUserLedgerControllerDB(t)
	accessToken := "exact-secret-token"
	customer := model.User{
		Username: "exact-customer", PublicID: "usr_public", Password: "password",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		AccessToken: &accessToken, Setting: `{"private":true}`,
	}
	admin := model.User{Username: "exact-admin", PublicID: "usr_admin", Password: "password", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	if err := db.Create(&[]*model.User{&customer, &admin}).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	request := func(account string) *httptest.ResponseRecorder {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		engine.GET("/lookup", LookupCustomerAccount)
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/lookup?account="+account, nil)
		engine.ServeHTTP(recorder, req)
		return recorder
	}
	for _, identifier := range []string{customer.Username, customer.PublicID} {
		body := request(identifier).Body.String()
		if !strings.Contains(body, `"success":true`) || !strings.Contains(body, customer.Username) {
			t.Fatalf("exact lookup %q failed: %s", identifier, body)
		}
		for _, secret := range []string{"exact-secret-token", `\"private\"`, "cus_exact_secret", "access_token", "stripe_customer", "setting"} {
			if strings.Contains(body, secret) {
				t.Fatalf("exact lookup leaked %q: %s", secret, body)
			}
		}
	}
	if body := request("exact").Body.String(); strings.Contains(body, `"success":true`) {
		t.Fatalf("fuzzy lookup must not match: %s", body)
	}
	if body := request(admin.Username).Body.String(); strings.Contains(body, `"success":true`) {
		t.Fatalf("lookup must not expose platform member: %s", body)
	}
}

func TestGetUserWalletLedgerEventsProjectsAuthoritativeSafeRows(t *testing.T) {
	db := setupUserLedgerControllerDB(t)
	user := model.User{Username: "ledger-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Create(&[]model.WalletLedger{
		{UserID: user.Id, DeltaMicrousd: 500, Kind: model.WalletLedgerKindRefund, RefType: "media_job", RefID: "private-refund-id", PricingVersion: 1, Metadata: `{"secret":"value"}`},
		{UserID: user.Id, DeltaMicrousd: -100, Kind: model.WalletLedgerKindSpend, RefType: "relay", RefID: "private-request-id", PricingVersion: 1},
	}).Error; err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	if err := db.Create(&model.AdminAuditLog{
		PublicID: "aud_safe", ActorUsername: "operator", Action: "user.manage.disable",
		TargetType: "user", TargetID: user.PublicID, DiffSummary: `{"password":{"before":"secret"}}`,
	}).Error; err != nil {
		t.Fatalf("seed account audit: %v", err)
	}

	recorder := newAdminRequest(common.RoleRootUser, user.Id, GetUserWalletLedgerEvents)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "private-refund-id") || strings.Contains(body, "secret") || strings.Contains(body, "diff_summary") {
		t.Fatalf("wallet projection leaked references or metadata: %s", body)
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    []struct {
			DeltaMicrousd int64  `json:"delta_microusd"`
			Kind          string `json:"kind"`
			Action        string `json:"action"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !parsed.Success || len(parsed.Data) != 2 || parsed.Data[0].Action != "user.manage.disable" || parsed.Data[1].Kind != model.WalletLedgerKindRefund || parsed.Data[1].DeltaMicrousd != 500 {
		t.Fatalf("unexpected wallet projection: %#v", parsed)
	}
}

func TestGetUserAccountSummaryToppedUpShowsAccountRateLimit(t *testing.T) {
	db := setupUserLedgerControllerDB(t)
	user := model.User{
		Username: "paid", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, HasToppedUp: true, RPMLimit: 42,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	recorder := newAdminRequest(common.RoleRootUser, user.Id, GetUserAccountSummary)
	var parsed struct {
		Data struct {
			RateLimit struct {
				RPM         int `json:"rpm"`
				TPM         int `json:"tpm"`
				RPMOverride int `json:"rpm_override"`
			} `json:"rate_limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode: %v (%s)", err, recorder.Body.String())
	}
	// A topped-up account runs at the paid default, with its own RPM override winning.
	if limit := parsed.Data.RateLimit; limit.RPM != 42 || limit.RPMOverride != 42 || limit.TPM != setting.RateLimitDefaultTPM {
		t.Fatalf("topped-up account should display 42 RPM / default TPM, got %#v", limit)
	}
}
