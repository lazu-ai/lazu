package router

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func markDeprecatedAlias(c *gin.Context, canonicalPath string) {
	c.Header("Deprecation", "true")
	if canonicalPath != "" {
		c.Header("Link", fmt.Sprintf("<%s>; rel=\"canonical\"", canonicalPath))
	}
}
