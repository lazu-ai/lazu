package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRetiredRoutes(t *testing.T) {
	engine := gin.New()
	SetRouter(engine)
	for _, route := range []string{
		"PUT /api/user/7/has_topped_up",
		"GET /api/data/self",
		"GET /api/data/",
		"GET /api/usage/token/",
		"GET /api/log/self/in_flight",
		"GET /api/log/self/tasks/1",
		"GET /api/models/rankings",
		"POST /api/models/rankings/recalculate",
		"GET /api/community/models",
		"GET /api/community/stats",
		"POST /api/community/export",
		"POST /pg/chat/completions",
		"GET /dashboard/billing/subscription",
		"GET /v1/dashboard/billing/usage",
		"GET /api/usage/token",
		"GET /api/community/models/example",
		"GET /dashboard/billing/usage",
		"GET /v1/dashboard/billing/subscription",
	} {
		method, path, _ := strings.Cut(route, " ")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", route, w.Code)
		}
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest("GET", "/api/announcements/", nil))
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/api/announcements" {
		t.Errorf("announcement redirect: %d, %v", w.Code, w.Header())
	}
}
