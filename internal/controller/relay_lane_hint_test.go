package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

// Agent clients print the error message and nothing else, so it has to carry the
// one action the customer can take, and it has to do so in words they have seen
// in the console — never the channel behind the lane.
func TestCheapLaneHintOnlyOnBusyFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		lane string
		code types.ErrorCode
		want bool
	}{
		{name: "agent lane overload", lane: model.ChannelLaneAgent, code: types.ErrorCodeUpstreamOverloaded, want: true},
		{name: "agent lane rate limit", lane: model.ChannelLaneAgent, code: types.ErrorCodeUpstreamRateLimited, want: true},
		{name: "agent lane connection drop", lane: model.ChannelLaneAgent, code: types.ErrorCodeUpstreamNetworkError, want: true},
		// The lane has nothing to do with a malformed request: telling the
		// customer to pay for the direct lane would be wrong advice.
		{name: "agent lane bad request", lane: model.ChannelLaneAgent, code: types.ErrorCodeBadRequestBody},
		{name: "agent lane blocked prompt", lane: model.ChannelLaneAgent, code: types.ErrorCodePromptBlocked},
		{name: "direct lane overload", lane: model.ChannelLaneDirect, code: types.ErrorCodeUpstreamOverloaded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			common.SetContextKey(c, constant.ContextKeyUsingGroup, tc.lane)
			lazuErr := types.NewErrorWithStatusCode(errors.New("boom"), tc.code, http.StatusBadGateway)
			annotateLaneFailure(c, lazuErr)
			// The hint has to land on the message clients display, not on the
			// raw_message detail an agent CLI never shows.
			shown := lazuErr.ToOpenAIError().Message
			if got := strings.Contains(shown, "pin the model to the direct lane"); got != tc.want {
				t.Fatalf("hint present = %v, want %v: %q", got, tc.want, shown)
			}
		})
	}
}

// Repeated annotation must not stack the sentence.
func TestCheapLaneHintIsNotDuplicated(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyUsingGroup, model.ChannelLaneAgent)
	lazuErr := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeUpstreamOverloaded, http.StatusBadGateway)
	annotateLaneFailure(c, lazuErr)
	annotateLaneFailure(c, lazuErr)
	shown := lazuErr.ToOpenAIError().Message
	if n := strings.Count(shown, "pin the model to the direct lane"); n != 1 {
		t.Fatalf("hint repeated %d times: %q", n, shown)
	}
	// Claude-format clients must get the same guidance.
	if !strings.Contains(lazuErr.ToClaudeError().Message, "pin the model to the direct lane") {
		t.Fatalf("Claude-format error lost the hint: %q", lazuErr.ToClaudeError().Message)
	}
}

// Operator vocabulary must not reach the customer's client.
func TestProviderFailureCopyAvoidsOperatorVocabulary(t *testing.T) {
	for _, code := range []types.ErrorCode{
		types.ErrorCodeUpstreamOverloaded,
		types.ErrorCodeUpstreamRateLimited,
		types.ErrorCodeUpstreamNetworkError,
		types.ErrorCodeUpstreamTimeout,
		// The default an unrecognised in-band stream failure now carries, and
		// the class a provider's own refusal maps to.
		types.ErrorCodeUpstreamError,
		types.ErrorCodePromptBlocked,
	} {
		_, _, message, _ := types.RenderError(code)
		for _, banned := range []string{"upstream", "channel", "provider"} {
			if strings.Contains(strings.ToLower(message), banned) {
				t.Fatalf("%s message exposes operator vocabulary %q: %q", code, banned, message)
			}
		}
	}
}
