package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/publicsite"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

// HTML, Markdown and the API all resolve the same administrator override.
// Do not cache legal text: a correction must be effective on the next read.
func PublicLegal(locale, kind string, markdown bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		settings := system_setting.GetLegalSettings()
		content := settings.PrivacyPolicyFor(locale)
		if kind == "terms" {
			content = settings.UserAgreementFor(locale)
		}
		c.Header("Cache-Control", "no-store")
		if markdown {
			c.Header("X-Robots-Tag", "noindex")
			c.Header("Link", "<"+publicsite.SiteOrigin+publicsite.LocalizedPath("/"+kind, locale)+">; rel=\"canonical\"")
			c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(content))
			return
		}
		body, err := publicsite.RenderLegal(content, locale, kind)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", body)
	}
}
