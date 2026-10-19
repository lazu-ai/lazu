package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func signedProxyRequest(t *testing.T, secret string, timestamp int64) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?stream=true", nil)
	requestID := common.RequestIDPrefix + "proxy-test"
	clientIP := "203.0.113.9"
	payload := proxySignaturePayload(timestamp, request.Method, request.URL.RequestURI(), clientIP, requestID)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	request.Header.Set(proxyTimestampHeader, strconv.FormatInt(timestamp, 10))
	request.Header.Set(proxySignatureHeader, hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set(proxyClientIPHeader, clientIP)
	request.Header.Set(proxyRequestIDHeader, requestID)
	return request
}

func TestInternalProxyAuthAcceptsSignedRequestAndPreservesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "proxy-secret-for-tests" // secret-scan: allow -- test fixture
	t.Setenv("INTERNAL_PROXY_SECRET", secret)
	engine := gin.New()
	engine.Use(InternalProxyAuth(), RequestId())
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"request_id": c.GetString(common.RequestIdKey)})
	})

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, signedProxyRequest(t, secret, time.Now().Unix()))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected signed request to pass, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("X-Request-Id"); got != common.RequestIDPrefix+"proxy-test" {
		t.Fatalf("expected forwarded request id, got %q", got)
	}
}

func TestInternalProxyAuthRejectsUnsignedAndExpiredRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "proxy-secret-for-tests" // secret-scan: allow -- test fixture
	t.Setenv("INTERNAL_PROXY_SECRET", secret)
	engine := gin.New()
	engine.Use(InternalProxyAuth())
	engine.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(http.StatusOK) })

	for name, request := range map[string]*http.Request{
		"unsigned": httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil),
		"expired":  signedProxyRequest(t, secret, time.Now().Add(-2*time.Minute).Unix()),
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("expected request to be rejected, got %d", recorder.Code)
			}
		})
	}
}

func TestInternalProxyAuthAllowsDirectSelfHostedRequestsWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INTERNAL_PROXY_SECRET", "")
	engine := gin.New()
	engine.Use(InternalProxyAuth())
	engine.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected self-hosted request to pass, got %d", recorder.Code)
	}
}

func TestInternalProxyAuthRecognizesSignedLoopbackRequestWhenExternalProxyDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INTERNAL_PROXY_SECRET", "")
	previousSessionSecret := common.SessionSecret
	common.SessionSecret = "session-secret-for-internal-relay-test" // secret-scan: allow -- test fixture
	t.Cleanup(func() {
		common.SessionSecret = previousSessionSecret
	})

	engine := gin.New()
	engine.Use(InternalProxyAuth())
	engine.POST("/v1/videos", func(c *gin.Context) {
		if !internalProxyVerified(c) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	request.Header.Set(proxyClientIPHeader, "203.0.113.10")
	request.Header.Set(proxyRequestIDHeader, common.RequestIDPrefix+"extension-relay")
	if err := SignInternalProxyRequest(request, time.Now()); err != nil {
		t.Fatalf("SignInternalProxyRequest error: %v", err)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected signed loopback request to be trusted, got %d", recorder.Code)
	}
}

func TestProxyRejectionCarriesFreshRequestIDInEveryProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INTERNAL_PROXY_SECRET", "proxy-secret-for-tests") // secret-scan: allow -- test fixture
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/api/user/self"} {
		t.Run(path, func(t *testing.T) {
			engine := gin.New()
			var contextID, requestContextID string
			engine.Use(func(c *gin.Context) {
				c.Next()
				contextID = c.GetString(common.RequestIdKey)
				requestContextID, _ = c.Request.Context().Value(common.RequestIdKey).(string)
			})
			engine.Use(InternalProxyAuth(), RequestId())
			engine.POST(path, func(c *gin.Context) { t.Error("rejected request reached handler") })
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.Header.Set("X-Request-Id", common.RequestIDPrefix+"untrusted")
			request.Header.Set(common.RequestIdKey, common.RequestIDPrefix+"untrusted")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			requestID := recorder.Header().Get(common.RequestIdKey)
			if requestID == "" || requestID == common.RequestIDPrefix+"untrusted" || recorder.Header().Get("X-Request-Id") != requestID || contextID != requestID || requestContextID != requestID {
				t.Fatalf("inconsistent request ID: headers=%v gin=%q request=%q", recorder.Header(), contextID, requestContextID)
			}
			var body struct {
				Type      string `json:"type"`
				Success   *bool  `json:"success"`
				RequestID string `json:"request_id"`
				Error     struct {
					Code, Type, Lazu string
					RequestID        string `json:"request_id"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusUnauthorized || body.Error.Code != "proxy_signature_invalid" || body.Error.Lazu == "" || body.Error.Lazu != recorder.Header().Get("X-Lazu-Error") {
				t.Fatalf("invalid error response: %d %s", recorder.Code, recorder.Body)
			}
			if path == "/v1/messages" {
				if body.Type != "error" || body.RequestID != requestID || body.Error.Type != "authentication_error" {
					t.Fatalf("invalid Anthropic response: %s", recorder.Body)
				}
			} else if body.Error.RequestID != requestID {
				t.Fatalf("missing nested ID: %s", recorder.Body)
			}
			if path == "/api/user/self" && (body.Success == nil || *body.Success) {
				t.Fatalf("invalid console response: %s", recorder.Body)
			}
		})
	}
}
