package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func configureMetadataEndpoints(t *testing.T) {
	t.Helper()
	t.Setenv("MODEL_METADATA_MODELS_URL", "https://metadata.example/models.json")
	t.Setenv("MODEL_METADATA_VENDORS_URL", "https://metadata.example/vendors.json")
	t.Setenv("MODEL_METADATA_I18N_MODELS_URL_TEMPLATE", "https://metadata.example/{locale}/models.json")
	t.Setenv("MODEL_METADATA_I18N_VENDORS_URL_TEMPLATE", "https://metadata.example/{locale}/vendors.json")
}

func TestMetadataEndpointSelection(t *testing.T) {
	for _, tc := range []struct {
		locale string
		path   string
	}{
		{"", ""}, {"unsupported", ""}, {"zh-TW", ""},
		{"en", "en/"}, {" ZH ", "zh/"}, {"ja", "ja/"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			configureMetadataEndpoints(t)
			models, vendors, err := getUpstreamURLs(tc.locale)
			if err != nil {
				t.Fatal(err)
			}
			if models != "https://metadata.example/"+tc.path+"models.json" || vendors != "https://metadata.example/"+tc.path+"vendors.json" {
				t.Fatalf("unexpected endpoints: %s, %s", models, vendors)
			}
		})
	}
}

func TestMissingLocalizedMetadataDoesNotDisableDefaultSync(t *testing.T) {
	configureMetadataEndpoints(t)
	t.Setenv("MODEL_METADATA_I18N_MODELS_URL_TEMPLATE", "")
	if _, _, err := getUpstreamURLs(""); err != nil {
		t.Fatalf("default metadata sync must remain available: %v", err)
	}
	if _, _, err := getUpstreamURLs("ja"); err == nil {
		t.Fatal("expected missing localized configuration to disable localized sync")
	}
}

func TestMetadataEndpointConfigurationRejectsPartialOrInvalidPairs(t *testing.T) {
	for _, tc := range []struct {
		name, key, value, locale string
	}{
		{"missing models", "MODEL_METADATA_MODELS_URL", "", ""},
		{"missing vendors", "MODEL_METADATA_VENDORS_URL", " ", ""},
		{"missing defaults with locale", "MODEL_METADATA_MODELS_URL", "", "zh"},
		{"relative URL", "MODEL_METADATA_MODELS_URL", "/models.json", ""},
		{"invalid scheme", "MODEL_METADATA_VENDORS_URL", "file:///vendors.json", ""},
		{"missing locale placeholder", "MODEL_METADATA_I18N_VENDORS_URL_TEMPLATE", "https://metadata.example/vendors.json", "en"},
		{"missing locale vendors", "MODEL_METADATA_I18N_VENDORS_URL_TEMPLATE", "", "en"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configureMetadataEndpoints(t)
			t.Setenv(tc.key, tc.value)
			if _, _, err := getUpstreamURLs(tc.locale); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestMetadataHandlersSkipUnconfiguredSyncBeforeDatabaseOrNetwork(t *testing.T) {
	configureMetadataEndpoints(t)
	t.Setenv("MODEL_METADATA_MODELS_URL", "")
	for _, tc := range []struct {
		name, method string
		handler      gin.HandlerFunc
	}{
		{"sync", http.MethodPost, SyncUpstreamModels},
		{"preview", http.MethodGet, SyncUpstreamPreview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, "/", strings.NewReader("{}"))
			c.Request.Header.Set("Content-Type", "application/json")
			tc.handler(c)
			var result struct {
				Success bool `json:"success"`
				Error   struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusServiceUnavailable || result.Success || result.Error.Code != "model_sync_not_configured" {
				t.Fatalf("expected an explicit skipped-sync response, got %d %s", w.Code, w.Body.String())
			}
			// The configuration detail (which variable, which URL) stays in the log.
			if strings.Contains(result.Error.Message, "MODEL_METADATA") || strings.Contains(result.Error.Message, "{locale}") {
				t.Fatalf("configuration detail leaked: %s", result.Error.Message)
			}
		})
	}
}
