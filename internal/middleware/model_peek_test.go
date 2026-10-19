package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

type authUnreadBody struct{ reads int }

func (b *authUnreadBody) Read([]byte) (int, error) {
	b.reads++
	return 0, io.ErrUnexpectedEOF
}
func (*authUnreadBody) Close() error { return nil }

func TestTokenAuthRejectsUnverifiedCredentialsWithoutReadingBody(t *testing.T) {
	fixture := authStageMetricsFixture(t)
	for _, key := range []string{"", "unknown-key", fixture.internal.Key} {
		body := &authUnreadBody{}
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Authorization", "Bearer "+key)
		}
		router := gin.New()
		router.POST("/v1/chat/completions", TokenAuth(), func(*gin.Context) { t.Error("unauthenticated request reached handler") })
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || body.reads != 0 {
			t.Fatalf("key=%q status=%d body reads=%d", key, response.Code, body.reads)
		}
	}
}

func TestTokenAuthKeepsModelForVerifiedInactiveKey(t *testing.T) {
	fixture := authStageMetricsFixture(t)
	if err := fixture.db.Model(&model.Token{}).Where("id = ?", fixture.token.Id).Update("status", common.TokenStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	var requestedModel string
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Next(); requestedModel = common.RequestedModelName(c) })
	router.POST("/v1/chat/completions", TokenAuth())
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"verified-model"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+fixture.token.Key)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "token_disabled") || requestedModel != "verified-model" {
		t.Fatalf("verified rejection lost model: status=%d model=%q body=%s", response.Code, requestedModel, response.Body)
	}
}

// peekRequestedModel exists so a verified TokenAuth-stage rejection (key_budget_exceeded, project_access_suspended) does not log an empty
// model_name: TokenAuth runs strictly before Distribute on every relay
// route, so Distribute's own capture of the model happens too late for a
// rejection raised in TokenAuth itself.
func TestPeekRequestedModelCapturesJSONBodyNonDestructively(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	peekRequestedModel(c)

	if got := common.GetContextKeyString(c, constant.ContextKeyRequestModel); got != "gpt-4o" {
		t.Fatalf("peekRequestedModel did not capture the model: got %q", got)
	}
	if got, err := io.ReadAll(c.Request.Body); err != nil || string(got) != body {
		t.Fatalf("direct Gin JSON reader lost body: got %q, error %v", got, err)
	}
	// The body must still be fully readable by the real parser that runs
	// later in Distribute — a peek that consumes the body would break every
	// successful request, not just rejections.
	modelRequest, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("getModelRequest after peek: %v", err)
	}
	if modelRequest.Model != "gpt-4o" {
		t.Fatalf("body was not reusable after peek: got model %q", modelRequest.Model)
	}
}

func TestPeekRequestedModelMatchesJSONCaseFoldingAndRejectsAmbiguity(t *testing.T) {
	for _, body := range []string{`{"model":"allowed","model":"forbidden"}`, `{"model":"allowed","MODEL":"forbidden"}`, `{"MoDeL":"allowed","model":"forbidden"}`} {
		writer := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(writer)
		c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(body))
		peekRequestedModel(c)
		if !c.IsAborted() || writer.Code != 400 {
			t.Fatalf("ambiguous model accepted: %s, status %d", body, writer.Code)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/search", strings.NewReader(`{"MODEL":"allowed"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	peekRequestedModel(c)
	modelRequest, _, err := getModelRequest(c)
	if err != nil || modelRequest.Model != "allowed" || common.GetContextKeyString(c, constant.ContextKeyRequestModel) != "allowed" {
		t.Fatalf("case folding differs from DTO: %#v, %v", modelRequest, err)
	}
}

func TestPeekRequestedModelReadsGeminiPath(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1beta/models/gemini-3.5-flash:generateContent", bytes.NewReader(nil))

	peekRequestedModel(c)

	if got := common.GetContextKeyString(c, constant.ContextKeyRequestModel); got != "gemini-3.5-flash" {
		t.Fatalf("peekRequestedModel did not read the Gemini path model: got %q", got)
	}
}

func TestPeekRequestedModelLeavesGetRequestsAlone(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/channel_health", nil)

	// Must not panic or error on a body-less GET through a route TokenAuth
	// also guards (e.g. /api/channel_health uses UserOrTokenAuth).
	peekRequestedModel(c)

	if got := common.GetContextKeyString(c, constant.ContextKeyRequestModel); got != "" {
		t.Fatalf("expected no model for a GET request, got %q", got)
	}
}

func TestPeekRequestedModelDoesNotOverwriteAnExistingValue(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyRequestModel, "already-set")
	body := `{"model":"should-not-win"}`
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	peekRequestedModel(c)

	if got := common.GetContextKeyString(c, constant.ContextKeyRequestModel); got != "already-set" {
		t.Fatalf("peekRequestedModel overwrote an existing value: got %q", got)
	}
}

func TestRequestedModelNameFallsBackToPeekedRequestModel(t *testing.T) {
	// Mirrors a key_budget_exceeded rejection: TokenAuth's peek is the only
	// thing that ever ran (Distribute never starts), so original_model is
	// still empty when the rejection is logged.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyRequestModel, "deepseek-v4-flash")

	if got := common.RequestedModelName(c); got != "deepseek-v4-flash" {
		t.Fatalf("RequestedModelName did not fall back to request_model: got %q", got)
	}
}
