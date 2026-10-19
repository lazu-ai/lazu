package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
)

func TestOfficialDecisionProbeBridgesKnownAnswer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitTokenEncoders()
	db := setupMediaJobControllerTestDB(t)
	seedMediaJobControllerAuth(t, db)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/decisions" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Errorf("wrong official request route or credential")
		}
		var wire dto.OpenAIDecisionRequest
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		if wire.Model != "gpt-6-luna" || len(wire.Questions) != 1 || wire.Questions[0].Type != "predicate" || wire.Questions[0].Name == nil || *wire.Questions[0].Name != "red_apple" {
			t.Errorf("known-answer probe was not bridged: %+v", wire)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[{"name":"red_apple","type":"predicate","probability":0.99}],"usage":{"input_tokens":30,"output_tokens":0,"total_tokens":30,"input_tokens_details":{"cached_tokens":10}}}`)
	}))
	defer server.Close()
	base := server.URL
	setting := `{"decision_dialect":"openai"}`
	channel := &model.Channel{Id: 911, Type: 1, Key: "fixture", Status: common.ChannelStatusEnabled, Models: dto.OpenAIDecisionModel, Group: model.ChannelLaneDirect, BaseURL: &base, Setting: &setting}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	// Persistence encrypts the in-memory key. The real probe starts from a
	// channel read, which decrypts it before selecting a provider credential.
	var err error
	channel, err = model.GetChannelById(channel.Id, true)
	if err != nil {
		t.Fatal(err)
	}
	result := testChannelForLane(t.Context(), channel, dto.OpenAIDecisionModel, "decisions", model.ChannelLaneDirect, false)
	if result.localErr != nil || result.lazuErr != nil || !result.providerAttempted || calls != 1 {
		t.Fatalf("official probe failed: %v %v attempted=%v calls=%d", result.localErr, result.lazuErr, result.providerAttempted, calls)
	}
	if !probeReplyHasDecision(result.body) || result.promptTokens != 30 || result.outputTokens != 0 {
		t.Fatalf("official known answer/usage invalid: %s", result.body)
	}
}
