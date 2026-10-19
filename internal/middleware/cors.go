package middleware

import (
	"net/url"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func CORS() gin.HandlerFunc {
	config := cors.Config{
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Accept",
			"Accept-Language",
			"Anthropic-Beta",
			"Anthropic-Version",
			"Authorization",
			"Cache-Control",
			"Content-Type",
			"If-None-Match",
			"OpenAI-Beta",
			"X-API-Key",
			"X-Goog-Api-Client",
			"X-Goog-Api-Key",
			"X-Lazu-Agent-Id",
			"X-Lazu-Locale",
			"X-Lazu-Session-Id",
			"X-Lazu-User",
			"X-Requested-With",
			"X-Session-Id",
			"X-Token-Id",
			"X-Webhook-Signature",
		},
		ExposeHeaders: []string{
			"Content-Disposition",
			"Deprecation",
			"Link",
			"Retry-After",
			"X-Lazu-Cost-Usd",
			"X-Lazu-Error",
			EffortAppliedHeader,
			"X-Lazu-Lane",
			"X-Lazu-Lane-Fallback",
			"X-Lazu-Models-Source",
			"X-Lazu-Node",
			"X-Lazu-Provider",
			"X-Lazu-Region",
			"X-Lazu-Request-Id",
			"X-Lazu-Warning",
			"X-Request-Id",
		},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	allowedOriginsRaw := common.GetEnvOrDefaultString("CORS_ALLOW_ORIGINS", "")
	if strings.TrimSpace(allowedOriginsRaw) != "" {
		origins := make([]string, 0, 8)
		for _, origin := range strings.Split(allowedOriginsRaw, ",") {
			normalized := strings.TrimSpace(origin)
			if normalized != "" {
				origins = append(origins, normalized)
			}
		}
		if len(origins) > 0 {
			config.AllowOrigins = origins
			config.AllowWildcard = true
			return cors.New(config)
		}
	}

	// Fallback is limited to loopback origins in local/dev usage.
	// In production, set CORS_ALLOW_ORIGINS explicitly.
	config.AllowOriginFunc = func(origin string) bool {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "" {
			return true
		}
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return false
		}
		host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}
	return cors.New(config)
}
