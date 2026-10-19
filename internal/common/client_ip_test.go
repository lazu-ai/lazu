package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func withTrustedProxyCIDRs(t *testing.T, cidrs string) {
	t.Helper()
	oldCIDRs := TrustedProxyCIDRs
	TrustedProxyCIDRs = cidrs
	t.Cleanup(func() {
		TrustedProxyCIDRs = oldCIDRs
	})
}

func testClientIPContext(remoteAddr string, headers map[string]string) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	c.Request = req
	return c
}

func TestGetRealClientIPRejectsForwardHeadersFromUntrustedRemote(t *testing.T) {
	c := testClientIPContext("8.8.8.8:12345", map[string]string{
		"CF-Connecting-IP": "1.1.1.1",
		"X-Real-IP":        "1.0.0.1",
		"X-Forwarded-For":  "9.9.9.9",
	})

	if got := GetRealClientIP(c); got != "8.8.8.8" {
		t.Fatalf("expected TCP peer IP, got %q", got)
	}
}

func TestGetRealClientIPTrustsReverseProxyHeadersFromLoopback(t *testing.T) {
	withTrustedProxyCIDRs(t, "127.0.0.1/8,::1/128")

	c := testClientIPContext("127.0.0.1:12345", map[string]string{
		"CF-Connecting-IP": "1.1.1.1",
		"X-Real-IP":        "1.0.0.1",
	})

	if got := GetRealClientIP(c); got != "1.1.1.1" {
		t.Fatalf("expected Cloudflare client IP from trusted proxy, got %q", got)
	}
}

func TestGetRealClientIPRejectsForwardHeadersFromPrivateRemoteByDefault(t *testing.T) {
	c := testClientIPContext("10.0.0.2:12345", map[string]string{
		"X-Forwarded-For": "10.0.0.3, 8.8.4.4, 1.1.1.1",
	})

	if got := GetRealClientIP(c); got != "10.0.0.2" {
		t.Fatalf("expected TCP peer IP from unconfigured private remote, got %q", got)
	}
}

func TestGetRealClientIPUsesFirstPublicForwardedForHopFromConfiguredProxy(t *testing.T) {
	withTrustedProxyCIDRs(t, "10.0.0.0/24")

	c := testClientIPContext("10.0.0.2:12345", map[string]string{
		"X-Forwarded-For": "10.0.0.3, 8.8.4.4, 1.1.1.1",
	})

	if got := GetRealClientIP(c); got != "8.8.4.4" {
		t.Fatalf("expected first public forwarded hop, got %q", got)
	}
}

func TestGetRealClientIPTrustsSingleConfiguredProxyIP(t *testing.T) {
	withTrustedProxyCIDRs(t, "192.0.2.10")

	c := testClientIPContext("192.0.2.10:12345", map[string]string{
		"X-Real-IP": "203.0.113.20",
	})

	if got := GetRealClientIP(c); got != "203.0.113.20" {
		t.Fatalf("expected configured proxy header IP, got %q", got)
	}
}

// Production chain: Caddy → nginx (172.28.0.10) → API. nginx stamps its own
// peer, the Docker gateway, into X-Real-IP; that hop must not become the
// client, or every visitor without CF-Connecting-IP shares one identity.
func TestGetRealClientIPSkipsProxyHopInXRealIP(t *testing.T) {
	withTrustedProxyCIDRs(t, "127.0.0.1/8,::1/128,172.28.0.1/32,172.28.0.10/32")

	c := testClientIPContext("172.28.0.10:5555", map[string]string{
		"X-Real-IP":       "172.28.0.1",
		"X-Forwarded-For": "203.0.113.7, 172.28.0.1",
	})

	if got := GetRealClientIP(c); got != "203.0.113.7" {
		t.Fatalf("expected the first public forwarded address, got %q", got)
	}
}
