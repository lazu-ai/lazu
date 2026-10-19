package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/types"
)

const (
	internalProxyVerifiedKey = "lazu_internal_proxy_verified"
	proxyTimestampHeader     = "X-Lazu-Proxy-Timestamp"
	proxySignatureHeader     = "X-Lazu-Proxy-Signature"
	proxyClientIPHeader      = "CF-Connecting-IP"
	proxyRequestIDHeader     = "X-Request-Id"
	proxySignatureMaxAge     = 60 * time.Second
)

// InternalProxyAuth verifies requests forwarded by the Cloudflare Worker when
// INTERNAL_PROXY_SECRET is configured. Self-hosted deployments keep their
// direct-access behavior by leaving the secret unset.
func InternalProxyAuth() gin.HandlerFunc {
	secret, proxyRequired := internalProxySigningSecret()
	return func(c *gin.Context) {
		if !proxyRequired {
			if hasProxySignature(c.Request) && validProxySignature(c, secret, time.Now()) {
				c.Set(internalProxyVerifiedKey, true)
			}
			c.Next()
			return
		}
		if !validProxySignature(c, secret, time.Now()) {
			setRequestID(c, common.GenerateRequestID())
			err := types.Err(types.ErrorCodeProxySignatureInvalid)
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				httperr.Abort(c, err)
			} else {
				httperr.AbortGateway(c, err)
			}
			return
		}
		c.Set(internalProxyVerifiedKey, true)
		c.Next()
	}
}

// SignInternalProxyRequest marks a server-originated loopback request with the
// same capability used by the external Worker proxy. When the deployment does
// not configure an external proxy secret, the process session secret keeps the
// capability unavailable to browser clients.
func SignInternalProxyRequest(request *http.Request, now time.Time) error {
	if request == nil {
		return errors.New("internal proxy request is unavailable")
	}
	secret, _ := internalProxySigningSecret()
	if secret == "" {
		return errors.New("internal proxy signing secret is unavailable")
	}
	timestamp := now.Unix()
	payload := proxySignaturePayload(
		timestamp,
		request.Method,
		request.URL.RequestURI(),
		request.Header.Get(proxyClientIPHeader),
		request.Header.Get(proxyRequestIDHeader),
	)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	request.Header.Set(proxyTimestampHeader, strconv.FormatInt(timestamp, 10))
	request.Header.Set(proxySignatureHeader, hex.EncodeToString(mac.Sum(nil)))
	return nil
}

func internalProxySigningSecret() (string, bool) {
	if secret := strings.TrimSpace(os.Getenv("INTERNAL_PROXY_SECRET")); secret != "" {
		return secret, true
	}
	return strings.TrimSpace(common.SessionSecret), false
}

func hasProxySignature(request *http.Request) bool {
	if request == nil {
		return false
	}
	return strings.TrimSpace(request.Header.Get(proxyTimestampHeader)) != "" ||
		strings.TrimSpace(request.Header.Get(proxySignatureHeader)) != ""
}

func validProxySignature(c *gin.Context, secret string, now time.Time) bool {
	if strings.TrimSpace(secret) == "" {
		return false
	}
	timestamp, err := strconv.ParseInt(strings.TrimSpace(c.GetHeader(proxyTimestampHeader)), 10, 64)
	if err != nil {
		return false
	}
	signedAt := time.Unix(timestamp, 0)
	age := now.Sub(signedAt)
	if age < -proxySignatureMaxAge || age > proxySignatureMaxAge {
		return false
	}

	provided, err := hex.DecodeString(strings.TrimSpace(c.GetHeader(proxySignatureHeader)))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	payload := proxySignaturePayload(
		timestamp,
		c.Request.Method,
		c.Request.URL.RequestURI(),
		c.GetHeader(proxyClientIPHeader),
		c.GetHeader(proxyRequestIDHeader),
	)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hmac.Equal(provided, mac.Sum(nil))
}

func proxySignaturePayload(timestamp int64, method, requestURI, clientIP, requestID string) string {
	return fmt.Sprintf(
		"%d\n%s\n%s\n%s\n%s",
		timestamp,
		strings.ToUpper(strings.TrimSpace(method)),
		requestURI,
		strings.TrimSpace(clientIP),
		strings.TrimSpace(requestID),
	)
}

func internalProxyVerified(c *gin.Context) bool {
	verified, _ := c.Get(internalProxyVerifiedKey)
	return verified == true
}
