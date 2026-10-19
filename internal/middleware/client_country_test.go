package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestCloudflareCountryTrustsOnlyAProxyAndCloudflaresShape(t *testing.T) {
	previous := common.TrustedProxyCIDRs
	common.TrustedProxyCIDRs = "127.0.0.1/32"
	t.Cleanup(func() { common.TrustedProxyCIDRs = previous })

	cases := []struct {
		remote, header, want string
	}{
		{"127.0.0.1:5000", "us", "US"},
		{"127.0.0.1:5000", "T1", "T1"},
		{"127.0.0.1:5000", "USA", ""},
		{"127.0.0.1:5000", "<x", ""},
		{"203.0.113.9:5000", "US", ""},
	}
	for _, tc := range cases {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		c.Request.RemoteAddr = tc.remote
		c.Request.Header.Set("CF-IPCountry", tc.header)
		if got := cloudflareCountry(c); got != tc.want {
			t.Fatalf("remote %s header %q: got %q, want %q", tc.remote, tc.header, got, tc.want)
		}
	}
}
