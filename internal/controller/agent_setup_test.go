package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

func TestGetAgentSetupScript(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := system_setting.ServerAddress
	t.Cleanup(func() { system_setting.ServerAddress = previous })

	router := gin.New()
	router.GET("/setup/:tool", GetAgentSetupScript)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "console.example.com"
		router.ServeHTTP(rec, req)
		return rec
	}

	system_setting.ServerAddress = "https://api.lazu.ai/"
	rec := get("/setup/claude-code")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "LAZU_BASE_URL='https://api.lazu.ai'") {
		t.Fatalf("script does not carry the configured server address:\n%.400s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("cache control = %q", rec.Header().Get("Cache-Control"))
	}

	rec = get("/setup/codex.ps1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "[string]$BaseUrl = 'https://api.lazu.ai'") {
		t.Fatalf("powershell script: %d %.300s", rec.Code, rec.Body.String())
	}

	if rec = get("/setup/cursor"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown tool status = %d", rec.Code)
	}

	// A deployment whose server address is not a usable URL falls back to the
	// request host and must not let a cache share that answer.
	system_setting.ServerAddress = ""
	rec = get("/setup/opencode")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "LAZU_BASE_URL='http://console.example.com'") {
		t.Fatalf("fallback base url: %d %.300s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("fallback cache control = %q", rec.Header().Get("Cache-Control"))
	}

	// config.yaml ships this address, so a deployment that never set its own
	// still has one. Writing it into an installer would point the agent at the
	// user's own machine, so a remote request gets the request host instead.
	system_setting.ServerAddress = "http://localhost:3000"
	rec = get("/setup/opencode")
	if !strings.Contains(rec.Body.String(), "LAZU_BASE_URL='http://console.example.com'") {
		t.Fatalf("localhost placeholder leaked into the installer:\n%.300s", rec.Body.String())
	}

	// A developer reaching the same deployment over loopback does mean it.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/setup/opencode", nil)
	req.Host = "localhost:3000"
	router.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "LAZU_BASE_URL='http://localhost:3000'") {
		t.Fatalf("loopback request lost its own address:\n%.300s", rec.Body.String())
	}

	// Behind a TLS terminator the request arrives over plain HTTP on the
	// internal hop; the installer must still carry the address the user typed,
	// port and all, or the agent would send its Key in the clear.
	system_setting.ServerAddress = ""
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/setup/opencode", nil)
	req.Host = "lazu-web"
	req.Header.Set("X-Forwarded-Proto", "https, http")
	req.Header.Set("X-Forwarded-Host", "console.example.com:8443")
	router.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "LAZU_BASE_URL='https://console.example.com:8443'") {
		t.Fatalf("forwarded origin lost:\n%.300s", rec.Body.String())
	}

	// A proxy's internal Host does not make its remote visitor local.
	for _, tool := range []string{"opencode", "codex.ps1"} {
		system_setting.ServerAddress = "http://localhost:3000"
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/setup/"+tool, nil)
		req.Host = "localhost:3000"
		req.Header.Set("X-Forwarded-Proto", "https, http")
		req.Header.Set("X-Forwarded-Host", "console.example.com:8443, localhost:3000")
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://console.example.com:8443") {
			t.Fatalf("internal host shadowed public origin: %d %.300s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("derived origin cached publicly: %s", rec.Header().Get("Cache-Control"))
		}
	}

	// Loopback is a range, not three spellings. None of these may be written
	// into an installer for a remote visitor.
	for _, local := range []string{
		"http://127.0.0.1:3000",
		"http://127.1.2.3:3000",
		"http://[0:0:0:0:0:0:0:1]:3000",
		"http://[::1]:3000",
		"http://localhost:3000",
	} {
		system_setting.ServerAddress = local
		rec = get("/setup/opencode")
		if !strings.Contains(rec.Body.String(), "LAZU_BASE_URL='http://console.example.com'") {
			t.Fatalf("%s was trusted for a remote request:\n%.200s", local, rec.Body.String())
		}
	}

	// IPv6-only deployments must render too.
	system_setting.ServerAddress = "https://[2001:db8::1]:8443"
	rec = get("/setup/opencode")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "LAZU_BASE_URL='https://[2001:db8::1]:8443'") {
		t.Fatalf("ipv6 base url: %d %.300s", rec.Code, rec.Body.String())
	}
}
