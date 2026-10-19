package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
)

func TestApplyEffortCapDefersUntilUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-5.2","reasoning_effort":"high"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	applyEffortCap(c, "", "low")
	if _, cached := c.Get(common.KeyRequestBody); cached {
		t.Fatal("policy must not mutate the pre-mapping request")
	}
	if common.GetContextKeyString(c, constant.ContextKeyEffortCapSource) != "member" {
		t.Fatal("context not recorded")
	}
}

func TestApplyEffortCapNoCapLeavesBodyAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"reasoning_effort":"max"}`))
	applyEffortCap(c, "", "")
	if _, cached := c.Get(common.KeyRequestBody); cached {
		t.Fatal("body read without a cap")
	}
}

func TestPinnedChannelCannotBypassMemberModelLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"forbidden-model"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "1")
	common.SetContextKey(c, constant.ContextKeySuppressRelayTelemetry, true)
	common.SetContextKey(c, constant.ContextKeyMemberModelLimit, map[string]bool{"allowed-model": true})
	Distribute()(c)
	if !c.IsAborted() || c.Writer.Status() != http.StatusForbidden {
		t.Fatalf("pinned request bypassed member policy: %d", c.Writer.Status())
	}
}

func TestCORSExposesAppliedEffort(t *testing.T) {
	t.Setenv("CORS_ALLOW_ORIGINS", "https://client.example")
	router := gin.New()
	router.Use(CORS())
	router.POST("/v1/responses", func(c *gin.Context) { c.Header(EffortAppliedHeader, "low"); c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("Origin", "https://client.example")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if !strings.Contains(strings.ToLower(w.Header().Get("Access-Control-Expose-Headers")), strings.ToLower(EffortAppliedHeader)) {
		t.Fatal("effort header hidden from browser")
	}
}

func TestMemberModelScopeNormalizesSupportedAliases(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyMemberModelLimit, map[string]bool{"claude-sonnet-4-20250514": true})
	if !memberModelAllows(c, "claude-sonnet-4-20250514#thinking") {
		t.Fatal("supported alias rejected")
	}
	if memberModelAllows(c, "claude-opus-4-20250514#thinking") {
		t.Fatal("unlisted model admitted")
	}
}

func TestAutoSelectionCannotBypassMemberModelLimit(t *testing.T) {
	cleanup := setupDistributorTestDB(t)
	defer cleanup()
	name := "gpt-auto-denied"
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "auto-scope", Models: name, Group: model.ChannelLaneDirect, Key: "test"}
	if err := model.CoreDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.CoreDB().Create(&model.Ability{Group: model.ChannelLaneDirect, Model: name, ChannelId: channel.Id, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := (&model.Model{ModelName: name, Status: 1, LifecycleStatus: model.ModelLifecycleActive}).Insert(); err != nil {
		t.Fatal(err)
	}
	model.RefreshPricing()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyTokenGroup, model.ChannelLaneDirect)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "Tier0")
	common.SetContextKey(c, constant.ContextKeySuppressRelayTelemetry, true)
	common.SetContextKey(c, constant.ContextKeyMemberModelLimit, map[string]bool{"auto": true})
	Distribute()(c)
	if !c.IsAborted() || c.Writer.Status() != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"model_not_allowed"`) {
		t.Fatalf("auto scope result: %d %s", c.Writer.Status(), w.Body.String())
	}
}

func TestMemberModelLimitTreatsEffortWordsAsPartOfTheName(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyMemberModelLimit, map[string]bool{"gpt-5.2": true, "qwen3.8-max": true})
	if !memberModelAllows(c, "qwen3.8-max") {
		t.Fatal("listed model denied")
	}
	for _, name := range []string{"gpt-5.2-high", "qwen3.8"} {
		if memberModelAllows(c, name) {
			t.Fatalf("unlisted model admitted: %s", name)
		}
	}
}
