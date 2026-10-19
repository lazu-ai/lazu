package channel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/helper"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/tidwall/gjson"
)

func TestUpstreamEffortUsesFinalModelAndProtocol(t *testing.T) {
	cases := []struct{ name, model, path, body, field, want, applied string }{
		{"mapped sparse ladder", "deepseek-v4-pro", "/v1/chat/completions", `{"model":"deepseek-v4-pro","reasoning_effort":"medium"}`, "reasoning_effort", "low", "low"},
		{"omitted chat", "gpt-5.2", "/v1/chat/completions", `{"model":"gpt-5.2","messages":[]}`, "reasoning_effort", "", ""},
		{"omitted responses", "gpt-5.2", "/v1/responses", `{"model":"gpt-5.2","input":"hi"}`, "reasoning.effort", "", ""},
		{"compatible Claude omission", "claude-opus-5", "/v1/chat/completions", `{"model":"claude-opus-5","messages":[]}`, "reasoning_effort", "medium", "medium"},
		{"chat bridged to claude", "claude-opus-5", "/v1/messages", `{"model":"claude-opus-5","messages":[]}`, "output_config.effort", "medium", "medium"},
		{"claude converted to chat", "gpt-5.2", "/v1/chat/completions", `{"model":"gpt-5.2","reasoning_effort":"max"}`, "reasoning_effort", "medium", "medium"},
		{"omitted Gemini", "gemini-3-flash", "/v1beta/models/gemini-3-flash:generateContent", `{"contents":[]}`, "generationConfig.thinkingConfig.thinkingLevel", "medium", "medium"},
		{"dynamic Gemini", "gemini-2.5-pro", "/v1beta/models/gemini-2.5-pro:generateContent", `{"generationConfig":{"thinkingConfig":{"thinkingBudget":-1}}}`, "generationConfig.thinkingConfig.thinkingBudget", "8192", "medium"},
		{"on off omission", "glm-5", "/v1/chat/completions", `{"model":"glm-5","messages":[]}`, "thinking.type", "disabled", "disabled"},
		{"on off precedence", "glm-5", "/v1/chat/completions", `{"model":"glm-5","reasoning_effort":"high","thinking":{"type":"enabled"}}`, "thinking.type", "disabled", "disabled"},
		{"explicit opt out", "gpt-5.2", "/v1/chat/completions", `{"model":"gpt-5.2","reasoning_effort":"none"}`, "reasoning_effort", "none", ""},
		{"non reasoning model", "gpt-4o", "/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`, "reasoning_effort", "", ""},
		{"embeddings untouched", "gpt-5.2", "/v1/embeddings", `{"model":"gpt-5.2","input":"hi"}`, "reasoning_effort", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			common.SetContextKey(c, constant.ContextKeyEffortCap, "medium")
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tc.model}}
			reader, err := capUpstreamEffort(c, info, "https://upstream.example"+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(reader)
			if got := gjson.GetBytes(body, tc.field).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s; %s", tc.field, got, tc.want, body)
			}
			if got := w.Header().Get("X-Lazu-Effort-Applied"); got != tc.applied {
				t.Fatalf("header %q want %q", got, tc.applied)
			}
		})
	}
}

func TestMappedAndOverriddenEffortCannotEscapeCap(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyEffortCap, "medium")
	c.Set("model_mapping", `{"auto":"deepseek-v4-pro"}`)
	info := &relaycommon.RelayInfo{OriginModelName: "auto", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "auto"}}
	req := &dto.GeneralOpenAIRequest{Model: "auto", ReasoningEffort: "medium"}
	if err := helper.ModelMappedHelper(c, info, req); err != nil {
		t.Fatal(err)
	}
	body, err := common.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err = relaycommon.ApplyParamOverride(body, map[string]interface{}{"reasoning_effort": "max"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := capUpstreamEffort(c, info, "https://upstream.example/v1/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	result, _ := io.ReadAll(out)
	if gjson.GetBytes(result, "reasoning_effort").String() != "low" {
		t.Fatalf("cap escaped: %s", result)
	}
}

func TestMinimalCapNeverSnapsUpward(t *testing.T) {
	for _, name := range []string{"claude-opus-5", "deepseek-v4-pro", "kimi-k3", "glm-5.3"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		common.SetContextKey(c, constant.ContextKeyEffortCap, "minimal")
		out, err := capUpstreamEffort(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: name}}, "https://upstream.example/v1/messages", strings.NewReader(`{"model":"`+name+`","thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(out)
		if gjson.GetBytes(body, "thinking.type").String() != "disabled" {
			t.Fatalf("%s exceeded cap: %s", name, body)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyEffortCap, "minimal")
	if _, err := capUpstreamEffort(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4.7"}}, "https://upstream.example/v1/responses", strings.NewReader(`{"model":"grok-4.7"}`)); err == nil {
		t.Fatal("unsupported hard cap silently exceeded")
	}
}

func TestEffortCapAtHTTPBoundaryAndRetryReset(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		want := "medium"
		if calls == 2 {
			want = "low"
		}
		if got := gjson.GetBytes(body, "output_config.effort").String(); got != want {
			t.Errorf("actual wire effort=%q want %q", got, want)
		}
		if r.ContentLength != int64(len(body)) {
			t.Error("stale content length")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c, info := compatContext(`{"model":"claude-opus-5","messages":[]}`)
	info.ChannelOtherSettings.ClaudeOmitUnusedSystemBetaModels = nil
	common.SetContextKey(c, constant.ContextKeyEffortCap, "medium")
	a := compatHTTPAdaptor{url: server.URL + "/v1/messages"}
	for _, effort := range []string{"max", "low"} {
		response, err := DoApiRequest(a, c, info, strings.NewReader(`{"model":"claude-opus-5","output_config":{"effort":"`+effort+`"},"messages":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if calls != 2 {
		t.Fatalf("upstream calls=%d", calls)
	}
	if got := common.GetContextKeyString(c, constant.ContextKeyEffortApplied); got != "" {
		t.Fatalf("stale retry effort=%q", got)
	}
	c, info = compatContext(`{"model":"grok-4.7","messages":[]}`)
	common.SetContextKey(c, constant.ContextKeyEffortCap, "minimal")
	a.url = server.URL + "/v1/chat/completions"
	_, err := DoApiRequest(a, c, info, strings.NewReader(`{"model":"grok-4.7","messages":[]}`))
	if err == nil || calls != 2 || common.GetContextKeyBool(c, constant.ContextKeyProviderAttempted) {
		t.Fatalf("unsatisfiable cap contacted upstream: calls=%d err=%v", calls, err)
	}
	wrapped := types.NewOpenAIError(err, types.ErrorCodeAPIConnectionError, http.StatusInternalServerError)
	if wrapped.StatusCode != http.StatusBadRequest || !types.IsSkipRetryError(wrapped) {
		t.Fatalf("lost policy error: %+v", wrapped)
	}
}

func TestCustomEndpointEffortUsesOutgoingMetadata(t *testing.T) {
	for _, tc := range []struct {
		family                textgen.ProtocolFamily
		dialect, model, field string
	}{
		{textgen.ProtocolOpenAIChat, "openai", "claude-opus-5", "reasoning_effort"},
		{textgen.ProtocolOpenAIResponses, "openai", "claude-opus-5", "reasoning.effort"},
		{textgen.ProtocolOpenAIChat, "anthropic", "claude-opus-5", "output_config.effort"},
		{textgen.ProtocolGemini, "gemini", "gemini-3-flash", "generationConfig.thinkingConfig.thinkingLevel"},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyEffortCap, "low")
		info := &relaycommon.RelayInfo{
			RelayFormat:  types.RelayFormatOpenAI,
			ChannelMeta:  &relaycommon.ChannelMeta{UpstreamModelName: tc.model},
			ProtocolPlan: &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Family: tc.family, Dialect: tc.dialect}},
		}
		out, err := capUpstreamEffort(c, info, "https://proxy.example/generate", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(out)
		if gjson.GetBytes(body, tc.field).String() != "low" {
			t.Fatalf("%s: %s", tc.family, body)
		}
	}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}
	if outgoingEffortFormat(info, "https://proxy.example/generate") != "chat" {
		t.Fatal("legacy custom endpoint skipped")
	}
	info.RelayFormat = types.RelayFormatClaude
	info.ChannelMeta = &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI}
	if outgoingEffortFormat(info, "https://proxy.example/generate") != "chat" {
		t.Fatal("legacy custom bridge used inbound format")
	}

}

func TestLaterDottedGPTMinimalCaps(t *testing.T) {
	for _, name := range []string{"gpt-5.3", "gpt-5.6", "openai/gpt-5.6"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyEffortCap, "minimal")
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: name}}
		out, err := capUpstreamEffort(c, info, "https://upstream.example/v1/responses", strings.NewReader(`{"reasoning":{"effort":"high"}}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(out)
		if gjson.GetBytes(body, "reasoning.effort").String() != "none" {
			t.Fatalf("unsupported minimal: %s %s", name, body)
		}
	}
	if model.SnapEffort("gpt-5.2-pro", "low") != "none" {
		t.Fatal("Pro exception lost")
	}
}

// Model IDs are opaque: a name that happens to end in an effort word is a real
// upstream model (qwen3.8-max, o3-mini-high), never "base model + effort".
func TestModelNamesEndingInEffortWordsPassThrough(t *testing.T) {
	for _, tc := range []struct{ name, path, body string }{
		{"qwen3.8-max", "/v1/chat/completions", `{"model":"qwen3.8-max","messages":[]}`},
		{"o3-mini-high", "/v1/chat/completions", `{"model":"o3-mini-high","messages":[]}`},
		{"gpt-6-sol-ultra", "/v1/responses", `{"model":"gpt-6-sol-ultra","input":"hi"}`},
	} {
		for _, cap := range []string{"", "medium"} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			if cap != "" {
				common.SetContextKey(c, constant.ContextKeyEffortCap, cap)
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: tc.name}}
			out, err := capUpstreamEffort(c, info, "https://upstream.example"+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(out)
			if got := gjson.GetBytes(body, "model").String(); got != tc.name {
				t.Fatalf("cap %q rewrote %s to %s: %s", cap, tc.name, got, body)
			}
		}
	}
}
