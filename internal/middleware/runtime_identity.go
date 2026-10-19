package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

// RuntimeIdentity exposes the execution node for production diagnostics. It
// contains no secret or infrastructure address.
func RuntimeIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.NodeName != "" {
			c.Header("X-Lazu-Node", common.NodeName)
		}
		if common.NodeRegion != "" {
			c.Header("X-Lazu-Region", common.NodeRegion)
		}
		if common.NodeProvider != "" {
			c.Header("X-Lazu-Provider", common.NodeProvider)
		}
		c.Next()
	}
}
