package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"golang.org/x/oauth2"
)

func setupSourceStarTest(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousSecret := common.SessionSecret
	previousID := common.GitHubStarClientID
	previousClientSecret := common.GitHubStarClientSecret
	previousRedirect := common.GitHubStarRedirectURL
	previousSource := common.AppConfig.Site.SourceURL
	previousFrontend := common.OIDCFrontendBaseURL
	t.Cleanup(func() {
		common.SessionSecret = previousSecret
		common.GitHubStarClientID = previousID
		common.GitHubStarClientSecret = previousClientSecret
		common.GitHubStarRedirectURL = previousRedirect
		common.AppConfig.Site.SourceURL = previousSource
		common.OIDCFrontendBaseURL = previousFrontend
	})
	common.SessionSecret = "placeholder-session-value-with-enough-entropy"
	common.GitHubStarClientID = "star-app-id"
	common.GitHubStarClientSecret = "placeholder-star-app-value"
	common.GitHubStarRedirectURL = "https://console.example.com/api/source/star/callback"
	common.AppConfig.Site.SourceURL = "https://github.com/example-org/example"
	common.OIDCFrontendBaseURL = "https://console.example.com"
}

// fakeGitHub stands in for github.com's token endpoint and api.github.com's
// starring endpoints. starred is what GET reports before the PUT.
func fakeGitHub(t *testing.T, starred bool) (*httptest.Server, *int32) {
	t.Helper()
	var puts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"placeholder-user-token","token_type":"bearer"}`))
		case r.URL.Path == "/user/starred/example-org/example" && r.Method == http.MethodGet:
			if r.Header.Get("Authorization") != "Bearer placeholder-user-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if starred || atomic.LoadInt32(&puts) > 0 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/user/starred/example-org/example" && r.Method == http.MethodPut:
			if r.ContentLength != 0 || r.Header.Get("User-Agent") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			atomic.AddInt32(&puts, 1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	previousEndpoint := githubStarEndpoint
	previousAPI := githubAPIBaseURL
	githubStarEndpoint = oauth2.Endpoint{AuthURL: server.URL + "/login/oauth/authorize", TokenURL: server.URL + "/login/oauth/access_token"}
	githubAPIBaseURL = server.URL
	t.Cleanup(func() {
		githubStarEndpoint = previousEndpoint
		githubAPIBaseURL = previousAPI
	})
	return server, &puts
}

func sourceStarEngine() *gin.Engine {
	engine := gin.New()
	engine.GET("/api/source/star", SourceStarStatus)
	engine.GET("/api/source/star/start", SourceStarStart)
	engine.GET("/api/source/star/callback", SourceStarCallback)
	return engine
}

func finishQuery(t *testing.T, recorder *httptest.ResponseRecorder) url.Values {
	t.Helper()
	if recorder.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d: %s", recorder.Code, recorder.Body.String())
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if location.Scheme+"://"+location.Host != "https://console.example.com" || location.Path != sourceStarCallbackPath {
		t.Fatalf("expected the console's star page, got %q", location.String())
	}
	return location.Query()
}

func TestSourceStarEnabledNeedsAnAppAndAGitHubRepository(t *testing.T) {
	setupSourceStarTest(t)
	if !sourceStarEnabled() {
		t.Fatal("configured app and github source should enable starring")
	}
	common.AppConfig.Site.SourceURL = "https://codeberg.org/example-org/example"
	if sourceStarEnabled() {
		t.Fatal("a source published elsewhere cannot be starred on github")
	}
	common.AppConfig.Site.SourceURL = "https://github.com/example-org/example.git"
	common.GitHubStarClientSecret = ""
	if sourceStarEnabled() {
		t.Fatal("an app without a secret cannot exchange the code")
	}
}

func TestSourceStarStartSendsTheBrowserToGitHubWithAState(t *testing.T) {
	setupSourceStarTest(t)
	recorder := httptest.NewRecorder()
	sourceStarEngine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/source/star/start?return=/zh", nil))
	if recorder.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", recorder.Code)
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if location.Host != "github.com" || location.Path != "/login/oauth/authorize" {
		t.Fatalf("expected github authorization, got %q", location.String())
	}
	query := location.Query()
	if query.Get("client_id") != "star-app-id" || query.Get("state") == "" {
		t.Fatalf("authorization is missing the app or state: %q", location.String())
	}
	if query.Get("redirect_uri") != common.GitHubStarRedirectURL {
		t.Fatalf("unexpected redirect_uri %q", query.Get("redirect_uri"))
	}
	if query.Has("scope") {
		t.Fatalf("a github app authorization carries no scope, got %q", query.Get("scope"))
	}
	var txCookie *http.Cookie
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sourceStarTransactionCookie {
			txCookie = cookie
		}
	}
	if txCookie == nil || txCookie.Path != "/api/source/star" || !txCookie.HttpOnly {
		t.Fatalf("expected a callback-scoped transaction cookie, got %+v", txCookie)
	}
}

func TestSourceStarStartWithoutAnAppReportsFailure(t *testing.T) {
	setupSourceStarTest(t)
	common.GitHubStarClientID = ""
	recorder := httptest.NewRecorder()
	sourceStarEngine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/source/star/start?return=/faq", nil))
	query := finishQuery(t, recorder)
	if query.Get("result") != sourceStarResultFailed || query.Get("return") != "/faq" || query.Has("popup") {
		t.Fatalf("unexpected outcome %q", query.Encode())
	}
}

func startThenCallback(t *testing.T, callbackQuery func(state string) string) *httptest.ResponseRecorder {
	t.Helper()
	engine := sourceStarEngine()
	startRecorder := httptest.NewRecorder()
	engine.ServeHTTP(startRecorder, httptest.NewRequest(http.MethodGet, "/api/source/star/start?return=/zh/faq&popup=1", nil))
	location, err := url.Parse(startRecorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse start redirect: %v", err)
	}
	state := location.Query().Get("state")
	request := httptest.NewRequest(http.MethodGet, "/api/source/star/callback?"+callbackQuery(state), nil)
	for _, cookie := range startRecorder.Result().Cookies() {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestSourceStarCallbackStarsOnceAndReturnsToThePage(t *testing.T) {
	setupSourceStarTest(t)
	_, puts := fakeGitHub(t, false)
	recorder := startThenCallback(t, func(state string) string { return "code=one-time&state=" + url.QueryEscape(state) })
	query := finishQuery(t, recorder)
	if query.Get("result") != sourceStarResultStarred || query.Get("return") != "/zh/faq" || query.Get("popup") != "1" {
		t.Fatalf("unexpected outcome %q", query.Encode())
	}
	if atomic.LoadInt32(puts) != 1 {
		t.Fatalf("expected one star request, got %d", atomic.LoadInt32(puts))
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sourceStarTransactionCookie && cookie.MaxAge >= 0 {
			t.Fatal("transaction cookie should be cleared by the callback")
		}
	}
}

func TestSourceStarCallbackReportsAnExistingStar(t *testing.T) {
	setupSourceStarTest(t)
	_, puts := fakeGitHub(t, true)
	recorder := startThenCallback(t, func(state string) string { return "code=one-time&state=" + url.QueryEscape(state) })
	if got := finishQuery(t, recorder).Get("result"); got != sourceStarResultAlready {
		t.Fatalf("expected %q, got %q", sourceStarResultAlready, got)
	}
	if atomic.LoadInt32(puts) != 0 {
		t.Fatal("a repository already starred should not be starred again")
	}
}

func TestSourceStarCallbackRefusesAForeignState(t *testing.T) {
	setupSourceStarTest(t)
	_, puts := fakeGitHub(t, false)
	recorder := startThenCallback(t, func(string) string { return "code=one-time&state=someone-elses" })
	query := finishQuery(t, recorder)
	if query.Get("result") != sourceStarResultFailed || query.Get("return") != "/zh/faq" {
		t.Fatalf("unexpected outcome %q", query.Encode())
	}
	if atomic.LoadInt32(puts) != 0 {
		t.Fatal("a mismatched state must not reach github")
	}
}

func TestSourceStarCallbackReportsADeclinedAuthorization(t *testing.T) {
	setupSourceStarTest(t)
	recorder := startThenCallback(t, func(string) string { return "error=access_denied" })
	query := finishQuery(t, recorder)
	if query.Get("result") != sourceStarResultCancelled || query.Get("return") != "/zh/faq" {
		t.Fatalf("unexpected outcome %q", query.Encode())
	}
}

func TestSourceStarCallbackWithoutATransactionFails(t *testing.T) {
	setupSourceStarTest(t)
	recorder := httptest.NewRecorder()
	sourceStarEngine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/source/star/callback?code=x&state=y", nil))
	query := finishQuery(t, recorder)
	if query.Get("result") != sourceStarResultFailed || query.Get("return") != "/" {
		t.Fatalf("unexpected outcome %q", query.Encode())
	}
}

func TestSanitizedSourceStarReturnKeepsOnlySitePaths(t *testing.T) {
	cases := map[string]string{
		"":                      "/",
		"/zh":                   "/zh",
		"/faq?x=1":              "/faq?x=1",
		"https://evil.example/": "/",
		"//evil.example":        "/",
		"/\\evil.example":       "/",
		"console":               "/",
		strings.Repeat("/a", 3): "/a/a/a",
	}
	for raw, want := range cases {
		if got := sanitizedSourceStarReturn(raw); got != want {
			t.Errorf("sanitizedSourceStarReturn(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSourceStarStatusIsPublicAndCacheable(t *testing.T) {
	setupSourceStarTest(t)
	for _, enabled := range []bool{true, false} {
		if !enabled {
			common.GitHubStarClientSecret = ""
		}
		recorder := httptest.NewRecorder()
		sourceStarEngine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/source/star", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", recorder.Code)
		}
		if !strings.Contains(recorder.Header().Get("Cache-Control"), "max-age=300") {
			t.Fatalf("expected a cacheable answer, got %q", recorder.Header().Get("Cache-Control"))
		}
		want := `"enabled":` + map[bool]string{true: "true", false: "false"}[enabled]
		if !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("expected %s in %s", want, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"source_url":"https://github.com/example-org/example"`) {
			t.Fatalf("expected the published source in %s", recorder.Body.String())
		}
	}
}
