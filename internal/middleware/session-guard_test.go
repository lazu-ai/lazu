package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"
)

func TestSessionCookieGuardKeepsValidCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	codecs := securecookie.CodecsFromPairs([]byte("current-secret"))
	encoded, err := securecookie.EncodeMulti("session", map[interface{}]interface{}{"uid": 1}, codecs...)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: encoded})
	c.Request = req

	SessionCookieGuard("session", codecs)(c)

	if got, err := c.Request.Cookie("session"); err != nil || got.Value != encoded {
		t.Fatalf("valid cookie must be preserved, err=%v", err)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("valid cookie must not be expired")
	}
}

func TestSessionCookieGuardDropsStaleCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldCodecs := securecookie.CodecsFromPairs([]byte("old-secret"))
	stale, err := securecookie.EncodeMulti("session", map[interface{}]interface{}{"uid": 1}, oldCodecs...)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	newCodecs := securecookie.CodecsFromPairs([]byte("rotated-secret"))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: stale})
	req.AddCookie(&http.Cookie{Name: "other", Value: "keep"})
	c.Request = req

	SessionCookieGuard("session", newCodecs)(c)

	if _, err := c.Request.Cookie("session"); err == nil {
		t.Fatalf("stale session cookie must be stripped from the request")
	}
	if got, err := c.Request.Cookie("other"); err != nil || got.Value != "keep" {
		t.Fatalf("unrelated cookie must be preserved")
	}
	expired := false
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "session" && ck.MaxAge < 0 {
			expired = true
		}
	}
	if !expired {
		t.Fatalf("stale cookie must be expired via Set-Cookie")
	}
}
