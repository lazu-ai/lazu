package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"
	"github.com/lazu-ai/lazu/internal/common"
)

// SessionCookieGuard drops a session cookie that can no longer be decoded —
// typically after a SESSION_SECRET rotation or once the cookie is past its
// 30-day signature window. It runs before gin-contrib/sessions so the stale
// cookie never reaches the session store, which would otherwise log
// "[sessions] ERROR! securecookie: the value is not valid" on every request
// that carries it.
func SessionCookieGuard(name string, codecs []securecookie.Codec) gin.HandlerFunc {
	return func(c *gin.Context) {
		ck, err := c.Request.Cookie(name)
		if err != nil || ck.Value == "" {
			c.Next()
			return
		}
		var values map[interface{}]interface{}
		if decErr := securecookie.DecodeMulti(name, ck.Value, &values, codecs...); decErr != nil {
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     name,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			stripRequestCookie(c.Request, name)
			common.SysDebug("cleared invalid session cookie")
		}
		c.Next()
	}
}

// stripRequestCookie removes a single named cookie from the request so that
// the downstream session store sees it as absent rather than malformed.
func stripRequestCookie(r *http.Request, name string) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, ck := range cookies {
		if ck.Name == name {
			continue
		}
		r.AddCookie(ck)
	}
}
