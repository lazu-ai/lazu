package router

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

// Every route the API retirement ADR marks answers with its deprecation date,
// its sunset and, when there is one, its successor. Requests carry valid root
// credentials so routes behind group-level auth are reached too.
func TestRetiringRoutesAnnounceDeprecationAndSunset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupModelCatalogRouteTestDB(t)
	defer cleanup()

	accessToken := "retire0access0token0fixture00001"
	root := &model.User{
		Id: 2101, Username: "retire_root", Password: "password123", DisplayName: "retire_root",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, AccessToken: &accessToken,
	}
	if err := model.CoreDB().Create(root).Error; err != nil {
		t.Fatalf("insert root: %v", err)
	}
	if err := (&model.Token{
		Id: 3101, UserId: root.Id, Key: "retiretoken", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 100,
	}).Insert(); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	// The /v1 routes sit behind channel distribution, which answers a request
	// naming no catalog model before any handler runs; seed one model so the
	// requests below reach their routes.
	if err := model.CoreDB().Create(&model.Channel{
		Id: 4101, Type: constant.ChannelTypeOpenAI, Key: "upstream-key", Status: common.ChannelStatusEnabled,
		Name: "retire-channel", Models: "retire-gpt", Group: model.ChannelLaneDirect,
	}).Error; err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if err := model.CoreDB().Create(&model.Ability{
		Group: model.ChannelLaneDirect, Model: "retire-gpt", ChannelId: 4101, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("insert ability: %v", err)
	}
	if err := (&model.Model{ModelName: "retire-gpt", Status: 1, LifecycleStatus: model.ModelLifecycleActive}).Insert(); err != nil {
		t.Fatalf("insert model: %v", err)
	}
	model.RefreshPricing()

	engine := gin.New()
	engine.Use(sessions.Sessions("retire-session", cookie.NewStore([]byte("retire-cookie-fixture"))))
	SetApiRouter(engine)
	SetDashboardRouter(engine)
	SetRelayRouter(engine)

	const (
		session = "session" // root access token
		apiKey  = "key"     // project API key
	)
	cases := []struct {
		method, path, auth string
		sunset             string
		successor          string
		body               string
	}{
		{"PUT", "/api/user/7/has_topped_up", session, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"GET", "/api/data/self", session, "Mon, 01 Feb 2027 00:00:00 GMT", "/api/data/dashboard/self", ""},
		{"GET", "/api/data/", session, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"GET", "/api/usage/token/", apiKey, "Mon, 01 Feb 2027 00:00:00 GMT", "/api/usage/summary", ""},
		{"GET", "/api/log/self/in_flight", session, "Mon, 01 Feb 2027 00:00:00 GMT", "/api/log/self/tasks", ""},
		{"GET", "/api/log/self/tasks/1", session, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"GET", "/api/models/rankings", "", "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"POST", "/api/models/rankings/recalculate", session, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"GET", "/api/community/models", "", "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"GET", "/api/community/stats", "", "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"POST", "/api/community/export", session, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"POST", "/pg/chat/completions", session, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"GET", "/dashboard/billing/subscription", apiKey, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
		{"GET", "/v1/dashboard/billing/usage", apiKey, "Mon, 01 Feb 2027 00:00:00 GMT", "", ""},
	}
	wantDeprecation := fmt.Sprintf("@%d", retirementNotice.Unix())
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			switch tc.auth {
			case session:
				req.Header.Set("Authorization", accessToken)
				req.Header.Set("X-Lazu-User", strconv.Itoa(root.Id))
			case apiKey:
				req.Header.Set("Authorization", "Bearer sk-retiretoken")
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound && w.Header().Get("Deprecation") == "" {
				t.Fatalf("route not registered: %d %s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Deprecation"); got != wantDeprecation {
				t.Fatalf("Deprecation = %q, want %q (status %d, body %s)", got, wantDeprecation, w.Code, w.Body.String())
			}
			if got := w.Header().Get("Sunset"); got != tc.sunset {
				t.Fatalf("Sunset = %q, want %q", got, tc.sunset)
			}
			wantLink := ""
			if tc.successor != "" {
				wantLink = fmt.Sprintf("<%s>; rel=\"successor-version\"", tc.successor)
			}
			if got := w.Header().Get("Link"); got != wantLink {
				t.Fatalf("Link = %q, want %q", got, wantLink)
			}
		})
	}
}

// A route that is not retiring carries none of the headers.
func TestCurrentRouteHasNoRetirementHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/models/catalog", nil))
	if w.Header().Get("Deprecation") != "" || w.Header().Get("Sunset") != "" {
		t.Fatalf("headers = %v", w.Header())
	}
}
