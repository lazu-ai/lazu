package controller

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/service"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecisionProbeWithSyntheticEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitTokenEncoders()
	db := setupMediaJobControllerTestDB(t)
	seedMediaJobControllerAuth(t, db)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"jev-1.13.0","answers":{"red_apple":{"type":"noul","noul":0.99}},"usage":{"input_tokens":30,"output_tokens":20}}`)
	}))
	defer server.Close()
	base := server.URL
	setting := `{"decision_dialect":"typesafe"}`
	channel := &model.Channel{Id: 910, Type: 1, Key: "fixture", Status: common.ChannelStatusEnabled, Models: "jev-1.13", Group: model.ChannelLaneDirect, BaseURL: &base, Setting: &setting}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	result := testChannelForLane(t.Context(), channel, "jev-1.13", "decisions", model.ChannelLaneDirect, false)
	if result.localErr != nil || result.lazuErr != nil || !result.providerAttempted {
		t.Fatalf("probe failed: %v %v attempted=%v", result.localErr, result.lazuErr, result.providerAttempted)
	}
	if !probeReplyHasDecision(result.body) || result.outputTokens != 20 {
		t.Fatalf("probe did not validate known answer/usage: %s", result.body)
	}
}
