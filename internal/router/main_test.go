package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/setting"
)

func TestUnmatchedRoutesUseErrorContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := setting.StripeReturnBase
	setting.StripeReturnBase = ""
	t.Cleanup(func() { setting.StripeReturnBase = previous })
	for _, frontend := range []string{"", "https://lazu.ai"} {
		t.Run(frontend, func(t *testing.T) {
			t.Setenv("FRONTEND_BASE_URL", frontend)
			engine := gin.New()
			engine.Use(middleware.RequestId())
			SetRouter(engine)
			for _, path := range []string{"/api", "/api/unknown", "/v1/unknown", "/v1beta/unknown", "/v1/messages", "/v1/messages/unknown"} {
				t.Run(path, func(t *testing.T) {
					w := httptest.NewRecorder()
					engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
					var body struct {
						Type      string `json:"type"`
						Success   *bool  `json:"success"`
						RequestID string `json:"request_id"`
						Error     struct {
							Code, Lazu, Docs string
							RequestID        string `json:"request_id"`
						} `json:"error"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					if w.Code != http.StatusNotFound || body.Error.Code != "endpoint_not_supported" || body.Error.Lazu == "" || body.Error.Docs == "" || body.Error.Lazu != w.Header().Get("X-Lazu-Error") {
						t.Fatalf("invalid error contract: %d %s", w.Code, w.Body)
					}
					rid := body.Error.RequestID
					if path == "/v1/messages" || path == "/v1/messages/unknown" {
						rid = body.RequestID
						if body.Type != "error" || body.Error.RequestID != "" {
							t.Fatalf("invalid Anthropic envelope: %s", w.Body)
						}
					}
					if rid == "" || rid != w.Header().Get("X-Lazu-Request-Id") || rid != w.Header().Get("X-Request-Id") {
						t.Fatalf("missing request ID: %s", w.Body)
					}
					if (path == "/api" || path == "/api/unknown") && (body.Success == nil || *body.Success) {
						t.Fatalf("invalid console envelope: %s", w.Body)
					}
				})
			}
		})
	}
}

func TestSetRouterRedirectsKnownBrowserRoutesToFrontend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("FRONTEND_BASE_URL", "https://lazu.ai/")

	tests := []struct {
		name     string
		method   string
		path     string
		location string
	}{
		{
			name:     "password reset preserves query",
			method:   http.MethodGet,
			path:     "/user/reset?email=user%40example.com&user_id=42&session_version=0&token=reset-token",
			location: "https://lazu.ai/user/reset?email=user%40example.com&user_id=42&session_version=0&token=reset-token",
		},
		{
			name:     "wallet callback redirects to console",
			method:   http.MethodGet,
			path:     "/console/wallet",
			location: "https://lazu.ai/console/wallet",
		},
		{
			name:     "existing checkout success preserves order reference",
			method:   http.MethodGet,
			path:     "/payment/success?project_id=prj_test&top_up_id=top_test",
			location: "https://lazu.ai/payment/success?project_id=prj_test&top_up_id=top_test",
		},
		{
			name:     "old API document link redirects to console",
			method:   http.MethodGet,
			path:     "/payment/document?project_id=prj_test&top_up_id=top_test&document=invoice",
			location: "https://lazu.ai/payment/document?project_id=prj_test&top_up_id=top_test&document=invoice",
		},
		{
			name:     "usage callback preserves query",
			method:   http.MethodGet,
			path:     "/console/usage?tab=logs",
			location: "https://lazu.ai/console/usage?tab=logs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			SetRouter(engine)

			req := httptest.NewRequest(test.method, test.path, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusPermanentRedirect {
				t.Fatalf("expected status %d, got %d", http.StatusPermanentRedirect, w.Code)
			}
			if location := w.Header().Get("Location"); location != test.location {
				t.Fatalf("expected redirect %q, got %q", test.location, location)
			}
			if cacheControl := w.Header().Get("Cache-Control"); cacheControl != "no-store" {
				t.Fatalf("expected no-store cache policy, got %q", cacheControl)
			}
		})
	}
}

func TestSetRouterDoesNotRedirectUnknownOrUnsafeRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("FRONTEND_BASE_URL", "https://lazu.ai")

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "unknown page", method: http.MethodGet, path: "/unknown"},
		{name: "browser route POST", method: http.MethodPost, path: "/user/reset"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			SetRouter(engine)

			req := httptest.NewRequest(test.method, test.path, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("expected status %d, got %d", http.StatusNotFound, w.Code)
			}
			if location := w.Header().Get("Location"); location != "" {
				t.Fatalf("expected no redirect, got %q", location)
			}
		})
	}
}

func TestCheckoutSuccessRedirectsWithConfiguredReturnBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("FRONTEND_BASE_URL", "")
	previous := setting.StripeReturnBase
	setting.StripeReturnBase = "https://lazu.ai"
	t.Cleanup(func() { setting.StripeReturnBase = previous })
	engine := gin.New()
	SetRouter(engine)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/payment/success?top_up_id=top_test", nil))
	if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != "https://lazu.ai/payment/success?top_up_id=top_test" {
		t.Fatalf("unexpected checkout redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestBrowserRedirectTargetRejectsAPIPaths(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/user/resett", nil)
	if destination, ok := browserRedirectTarget(req, "https://lazu.ai"); ok {
		t.Fatalf("expected API typo not to redirect, got %q", destination)
	}
}

func TestSetRouterLeavesUnknownRouteUnredirectedWithoutValidFrontend(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, frontendBaseURL := range []string{"", "javascript:alert(1)", "://invalid"} {
		t.Run(frontendBaseURL, func(t *testing.T) {
			t.Setenv("FRONTEND_BASE_URL", frontendBaseURL)

			engine := gin.New()
			SetRouter(engine)

			req := httptest.NewRequest(http.MethodGet, "/user/reset", nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("expected status %d, got %d", http.StatusNotFound, w.Code)
			}
			if location := w.Header().Get("Location"); location != "" {
				t.Fatalf("expected no redirect, got %q", location)
			}
		})
	}
}

// Link checkers, SEO audits and some crawlers probe with HEAD; the public
// pages used to answer it with 404.
func TestPublicSitePagesAnswerHead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetPublicSiteRouter(engine)
	routes := engine.Routes()
	for _, path := range []string{
		"/models.md",
		"/privacy",
		"/zh/terms.md",
		"/models",
		"/models/:vendor",
		"/models/:vendor/:model",
		"/zh/models/:vendor/:model",
	} {
		if !hasRoute(routes, http.MethodGet, path) || !hasRoute(routes, http.MethodHead, path) {
			t.Errorf("%s: want GET and HEAD registered", path)
		}
	}
}
