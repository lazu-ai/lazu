package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
)

// A Key outlives its holder's deletion as a row. Its next request is refused
// because the account was deleted, and says so: project_access_suspended
// would send the holder to a project admin about a project they left.
func TestTokenAuthNamesADeletedHolder(t *testing.T) {
	fixture := authStageMetricsFixture(t)
	if _, err := model.DeleteSelfUser(fixture.user.Id); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handled := false
	router.POST("/v1/chat/completions", TokenAuth(), func(c *gin.Context) { handled = true; c.Status(http.StatusOK) })
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if handled {
		t.Fatal("a deleted holder's Key reached the handler")
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusForbidden || consoleErrorCode(payload) != "account_deleted" {
		t.Fatalf("deleted holder's Key answered status=%d body=%s", response.Code, response.Body.String())
	}
}
