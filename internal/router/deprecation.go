package router

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func markDeprecatedAlias(c *gin.Context, canonicalPath string) {
	c.Header("Deprecation", "true")
	if canonicalPath != "" {
		c.Header("Link", fmt.Sprintf("<%s>; rel=\"canonical\"", canonicalPath))
	}
}

// Routes on their way out answer with when they were deprecated (RFC 9745),
// when they go (RFC 8594) and, when there is one, what replaces them. The
// windows are 30 days for admin routes and 90 days for user routes and public
// API shapes, counted from the notice; production access is checked before
// each removal. If the notice ships after retirementNotice, move all three
// dates by the delay.
var (
	retirementNotice = time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	// Deprecated: delete after 2026-12-01 (admin routes marked with adminSunset).
	adminSunset = time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	// Deprecated: delete after 2027-02-01 (user and public routes marked with userSunset).
	userSunset = time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC)
)

// retiring marks every response of a route that is on its way out. successor
// is the path that replaces it, or "" when nothing does.
func retiring(sunset time.Time, successor string) gin.HandlerFunc {
	deprecation := fmt.Sprintf("@%d", retirementNotice.Unix())
	sunsetHeader := sunset.UTC().Format(http.TimeFormat)
	link := ""
	if successor != "" {
		link = fmt.Sprintf("<%s>; rel=\"successor-version\"", successor)
	}
	return func(c *gin.Context) {
		c.Header("Deprecation", deprecation)
		c.Header("Sunset", sunsetHeader)
		if link != "" {
			c.Header("Link", link)
		}
		c.Next()
	}
}
