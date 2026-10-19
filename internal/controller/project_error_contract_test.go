package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
)

// Every team-project and wallet failure leaves through httperr: a real status
// and a catalog code under error.code, never a 200 or a bare top-level code.
func TestProjectAndWalletFailuresUseTheErrorCatalog(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.ProjectInvite{}, &model.BudgetRequest{}, &model.TopUp{}, &model.Log{}); err != nil {
		t.Fatal(err)
	}
	oldLogDB := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(oldLogDB) })
	t.Setenv("PROJECTS_ENABLED", "true")
	users := []model.User{
		{Id: 31, Username: "contract-owner", Role: common.RoleCommonUser},
		{Id: 32, Username: "contract-admin", Role: common.RoleCommonUser},
		{Id: 33, Username: "contract-member", Role: common.RoleCommonUser},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	var owner model.User
	if err := db.First(&owner, 31).Error; err != nil {
		t.Fatal(err)
	}
	var project model.Project
	if err := db.Where("owner_user_id = ?", 31).First(&project).Error; err != nil {
		t.Fatal(err)
	}
	for _, m := range []model.ProjectMember{
		{ProjectID: project.ID, UserID: 32, Role: model.ProjectRoleAdmin, Status: model.ProjectMemberActive},
		{ProjectID: project.ID, UserID: 33, Role: model.ProjectRoleMember, Status: model.ProjectMemberActive},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}

	router := func(userID int) *gin.Engine {
		engine := gin.New()
		engine.Use(func(c *gin.Context) { c.Set("id", userID) })
		routes := engine.Group("/api/project/:id", middleware.ProjectConsoleContext())
		routes.PUT("", UpdateProject)
		routes.POST("/dissolve", DissolveProject)
		routes.GET("/members", ListProjectMembers)
		routes.GET("/members/:uid", GetProjectMember)
		routes.PUT("/members/:uid/role", UpdateProjectMemberRole)
		routes.POST("/invites", CreateProjectInvite)
		routes.POST("/budget-requests/:rid/approve", ApproveProjectBudgetRequest)
		routes.GET("/tokens", ListProjectTokens)
		routes.GET("/attention", GetProjectAttention)
		routes.GET("/topups/:topup_id", GetProjectTopUp)
		engine.POST("/api/invite/:token/accept", AcceptProjectInvite)
		engine.GET("/api/user/payments/:topup_id", GetOwnPaymentRecord)
		engine.POST("/api/user/stripe/pay", RequestStripePay)
		engine.POST("/api/user/payment-disputes/:topup_id/resolve", AdminResolvePaymentDispute)
		return engine
	}
	base := "/api/project/" + project.PublicID
	cases := []struct {
		name, method, path, body string
		user, status             int
		code, param              string
	}{
		{"update without version", "PUT", base, `{"name":"x"}`, 31, 400, "invalid_request", "expected_version"},
		{"member edits project", "PUT", base, `{"name":"x","expected_version":1}`, 33, 403, "access_denied", ""},
		{"dissolve with wrong name", "POST", base + "/dissolve", `{"confirm_name":"nope"}`, 31, 400, "confirm_mismatch", ""},
		{"bad pagination", "GET", base + "/members?page=0", "", 31, 400, "invalid_pagination", ""},
		{"unknown member", "GET", base + "/members/usr_missing", "", 31, 404, "project_member_not_found", ""},
		{"owner role change", "PUT", base + "/members/" + owner.PublicID + "/role", `{"role":"member"}`, 32, 403, "cannot_change_owner", ""},
		{"invalid invite email", "POST", base + "/invites", `{"email":"not-an-email","role":"member"}`, 32, 400, "invite_email_invalid", ""},
		{"unknown invitation", "POST", "/api/invite/missing-token/accept", "", 33, 404, "invite_not_found", ""},
		{"unknown budget request", "POST", base + "/budget-requests/br_missing/approve", `{}`, 31, 404, "budget_request_not_found", ""},
		{"member lists keys", "GET", base + "/tokens", "", 33, 403, "access_denied", ""},
		{"member reads attention", "GET", base + "/attention", "", 33, 403, "access_denied", ""},
		{"unknown project top-up", "GET", base + "/topups/top_missing", "", 31, 404, "payment_not_found", ""},
		{"unknown own payment", "GET", "/api/user/payments/top_missing", "", 33, 404, "payment_not_found", ""},
		{"mismatched project selectors", "POST", "/api/user/stripe/pay?project_id=prj_other", `{"amount":10,"payment_method":"stripe","project_id":"` + project.PublicID + `"}`, 31, 400, "project_context_mismatch", ""},
		{"unknown dispute outcome", "POST", "/api/user/payment-disputes/top_1/resolve", `{"dispute_id":"dp_1","outcome":"maybe"}`, 31, 400, "invalid_request", "outcome"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router(tc.user).ServeHTTP(w, req)
			body := assertConsoleError(t, w, tc.status, tc.code)
			if tc.param != "" && body["param"] != tc.param {
				t.Fatalf("expected param %q, got %s", tc.param, w.Body.String())
			}
			if lazu, _ := body["lazu"].(string); !strings.HasPrefix(lazu, "LZ-") {
				t.Fatalf("failure has no LZ number: %s", w.Body.String())
			}
		})
	}

	// Log filters name a missing member after the member, not a bare 404.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/log/self?user_id=usr_missing", nil)
	c.Set("id", 31)
	if _, ok := projectLogAccess(c); ok {
		t.Fatal("unknown member filter was accepted")
	}
	assertConsoleError(t, w, http.StatusNotFound, "project_member_not_found")
}

// /v1/dashboard/billing answers in the OpenAI envelope with a real status; the
// cause of a failed read stays in the server log.
func TestDashboardBillingFailureUsesOpenAIEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMediaJobControllerTestDB(t)
	previous := common.DisplayTokenStatEnabled
	common.DisplayTokenStatEnabled = true
	t.Cleanup(func() { common.DisplayTokenStatEnabled = previous })
	for name, handler := range map[string]gin.HandlerFunc{"subscription": GetSubscription, "usage": GetUsage} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/dashboard/billing/"+name, nil)
		// The authenticated Key vanished between auth and the read.
		c.Set("token_id", 987654)
		handler(c)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: expected 500, got %d %s", name, w.Code, w.Body.String())
		}
		var payload struct {
			Success *bool `json:"success"`
			Error   struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Success != nil || payload.Error.Code != "internal_error" || payload.Error.Type != "api_error" {
			t.Fatalf("%s: unexpected envelope %s", name, w.Body.String())
		}
	}
}
