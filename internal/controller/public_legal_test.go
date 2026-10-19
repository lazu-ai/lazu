package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

func TestLegalOverrideIsCurrentInHTMLMarkdownAndAPI(t *testing.T) {
	settings := system_setting.GetLegalSettings()
	previous := settings.PrivacyPolicyEn
	t.Cleanup(func() { settings.PrivacyPolicyEn = previous })
	router := gin.New()
	router.GET("/privacy", PublicLegal("en", "privacy", false))
	router.GET("/privacy.md", PublicLegal("en", "privacy", true))
	router.GET("/api/privacy-policy", GetPrivacyPolicy)
	for _, content := range []string{"# Privacy\n\nFirst effective version.", "# Privacy\n\nUpdated effective version."} {
		settings.PrivacyPolicyEn = content
		for _, path := range []string{"/privacy", "/privacy.md", "/api/privacy-policy?lang=en"} {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
			if recorder.Code != 200 || recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s: %d %v", path, recorder.Code, recorder.Header())
			}
			body := recorder.Body.String()
			if strings.HasPrefix(path, "/api/") {
				var payload struct {
					Data string `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				body = payload.Data
			}
			if path == "/privacy" {
				if !strings.Contains(body, strings.Split(content, "\n\n")[1]) {
					t.Fatal("HTML omitted effective text")
				}
			} else if body != content {
				t.Fatalf("source diverged for %s", path)
			}
		}
	}
}
