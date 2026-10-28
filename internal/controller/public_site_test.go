package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/publicsite"
)

func withPublicCatalog(t *testing.T, models ...publicsite.Model) {
	t.Helper()
	publicCatalogCache.Lock()
	previous, previousExpiry := publicCatalogCache.catalog, publicCatalogCache.expiresAt
	publicCatalogCache.catalog = publicsite.Catalog{Models: models, UpdatedAt: time.Now().UTC()}
	publicCatalogCache.expiresAt = time.Now().Add(time.Hour)
	publicCatalogCache.Unlock()
	t.Cleanup(func() {
		publicCatalogCache.Lock()
		publicCatalogCache.catalog, publicCatalogCache.expiresAt = previous, previousExpiry
		publicCatalogCache.Unlock()
	})
}

// A model id may contain a slash; its page lives at /models/<maker>/<id>
// with the slash kept, which is the path ModelPath links and the sitemap
// lists.
func TestPublicModelPageServesIdsWithASlash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withPublicCatalog(t, publicsite.Model{ID: "BAAI/bge-m3", Maker: "baai"})
	engine := gin.New()
	engine.GET("/models/:vendor/*model", PublicModelPage("en"))

	cases := []struct {
		path, wantType, wantLocation string
		wantStatus                   int
	}{
		{path: "/models/baai/BAAI/bge-m3.md", wantStatus: http.StatusOK, wantType: "text/markdown"},
		{path: "/models/baai/BAAI/bge-m3.json", wantStatus: http.StatusOK, wantType: "application/json"},
		{path: "/models/other/BAAI/bge-m3.md", wantStatus: http.StatusMovedPermanently, wantLocation: "/models/baai/BAAI/bge-m3.md"},
		{path: "/models/baai/", wantStatus: http.StatusMovedPermanently, wantLocation: "/models/baai"},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if recorder.Code != tc.wantStatus {
			t.Fatalf("%s: status %d, want %d: %s", tc.path, recorder.Code, tc.wantStatus, recorder.Body.String())
		}
		if tc.wantType != "" && !strings.HasPrefix(recorder.Header().Get("Content-Type"), tc.wantType) {
			t.Fatalf("%s: content type %q, want %s", tc.path, recorder.Header().Get("Content-Type"), tc.wantType)
		}
		if got := recorder.Header().Get("Location"); got != tc.wantLocation {
			t.Fatalf("%s: location %q, want %q", tc.path, got, tc.wantLocation)
		}
	}
	if path := publicsite.ModelPath(publicsite.Model{ID: "BAAI/bge-m3", Maker: "baai"}); path != "/models/baai/BAAI/bge-m3" {
		t.Fatalf("ModelPath = %q", path)
	}
}
