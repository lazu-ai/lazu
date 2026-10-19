package middleware

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func RequestId() func(c *gin.Context) {
	return func(c *gin.Context) {
		id := common.GenerateRequestID()
		if internalProxyVerified(c) {
			if forwarded := strings.TrimSpace(c.GetHeader(proxyRequestIDHeader)); validForwardedRequestID(forwarded) {
				id = forwarded
			}
		}
		setRequestID(c, id)
		c.Next()
	}
}

func setRequestID(c *gin.Context, id string) {
	c.Set(common.RequestIdKey, id)
	ctx := context.WithValue(c.Request.Context(), common.RequestIdKey, id)
	c.Request = c.Request.WithContext(ctx)
	c.Header(common.RequestIdKey, id)
	c.Header("X-Request-Id", id)
}

func validForwardedRequestID(value string) bool {
	if !strings.HasPrefix(value, common.RequestIDPrefix) || len(value) > 96 {
		return false
	}
	for _, character := range value[len(common.RequestIDPrefix):] {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' {
			return false
		}
	}
	return len(value) > len(common.RequestIDPrefix)
}
