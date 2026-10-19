package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestRuntimeIdentityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousName := common.NodeName
	previousRegion := common.NodeRegion
	previousProvider := common.NodeProvider
	common.NodeName = "node-sv-api"
	common.NodeRegion = "us-west"
	common.NodeProvider = "example-cloud"
	t.Cleanup(func() {
		common.NodeName = previousName
		common.NodeRegion = previousRegion
		common.NodeProvider = previousProvider
	})

	engine := gin.New()
	engine.Use(RuntimeIdentity())
	engine.GET("/livez", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if got := recorder.Header().Get("X-Lazu-Node"); got != "node-sv-api" {
		t.Fatalf("unexpected node header %q", got)
	}
	if got := recorder.Header().Get("X-Lazu-Region"); got != "us-west" {
		t.Fatalf("unexpected region header %q", got)
	}
	if got := recorder.Header().Get("X-Lazu-Provider"); got != "example-cloud" {
		t.Fatalf("unexpected provider header %q", got)
	}
}
