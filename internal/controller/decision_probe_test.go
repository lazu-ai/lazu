package controller

import (
	"encoding/json"
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
	expectedModel := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != expectedModel {
			t.Errorf("probed %s instead of %s", payload.Model, expectedModel)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"jev-1.14.0","answers":{"red_apple":{"type":"noul","noul":0.99}},"usage":{"input_tokens":30,"output_tokens":20}}`)
	}))
	defer server.Close()
	base := server.URL
	setting := `{"decision_dialect":"typesafe"}`
	channel := &model.Channel{Id: 910, Type: 1, Key: "fixture", Status: common.ChannelStatusEnabled, Models: "jev-1.14", Group: model.ChannelLaneDirect, BaseURL: &base, Setting: &setting}
	if err := db.Create(channel).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ public, mapping, upstream string }{
		{"jev-1.13", "", "jev-1.13.0"}, {"jev-1.14", "", "jev-1.14.0"},
		{"jev-1.14.0", "", "jev-1.14.0"}, {"jev-latest", "", "jev-latest"},
		{"jev-1.14", `{"jev-1.14":"custom-pinned-model"}`, "custom-pinned-model"},
	} {
		t.Run(tc.public+tc.mapping, func(t *testing.T) {
			expectedModel = tc.upstream
			channel.Models, channel.ModelMapping = tc.public, &tc.mapping
			result := testChannelForLane(t.Context(), channel, tc.public, "decisions", model.ChannelLaneDirect, false)
			if result.localErr != nil || result.lazuErr != nil || !result.providerAttempted {
				t.Fatalf("probe failed: %v %v attempted=%v", result.localErr, result.lazuErr, result.providerAttempted)
			}
			if !probeReplyHasDecision(result.body) || result.outputTokens != 20 {
				t.Fatalf("probe lost answer/usage: %s", result.body)
			}
		})
	}
}
