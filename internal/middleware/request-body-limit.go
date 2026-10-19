package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// Align JSON relay requests with the 64 MiB frontend proxy ceiling while
// allowing large inline multimodal payloads to reach providers that support
// them. MaxBytesReader still applies to the decompressed stream.
const DefaultRelayRequestBodyLimitBytes = int64(64 << 20)

func RelayRequestBodyLimit() gin.HandlerFunc {
	return relayRequestBodyLimit(DefaultRelayRequestBodyLimitBytes, common.MaxMultipartMemory)
}

func relayRequestBodyLimit(jsonLimit int64, multipartLimit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.Body == nil {
			c.Next()
			return
		}

		limit := relayBodyLimitForContentType(c.GetHeader("Content-Type"), jsonLimit, multipartLimit)
		if limit <= 0 {
			c.Next()
			return
		}

		if c.Request.ContentLength > limit {
			abortWithCode(c, types.ErrorCodeRequestBodyTooLarge)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

func relayBodyLimitForContentType(contentType string, jsonLimit int64, multipartLimit int64) int64 {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(contentType, "multipart/") {
		return multipartLimit
	}
	return jsonLimit
}
