package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestChannelRetryCapacityKeepsMultiKeyChannelEligible(t *testing.T) {
	tests := []struct {
		name    string
		channel *model.Channel
		want    int
	}{
		{name: "single key", channel: &model.Channel{Key: "one"}, want: 1},
		{name: "three polling keys", channel: &model.Channel{Key: "one\ntwo\nthree", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModePolling}}, want: 3},
		{name: "one disabled polling key", channel: &model.Channel{Key: "one\ntwo\nthree", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModePolling, MultiKeyStatusList: map[int]int{1: common.ChannelStatusManuallyDisabled}}}, want: 2},
		{name: "all disabled polling fallback", channel: &model.Channel{Key: "one\ntwo", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling, MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusManuallyDisabled}}}, want: 1},
		{name: "random keys cannot guarantee rotation", channel: &model.Channel{Key: "one\ntwo\nthree", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 3, MultiKeyMode: constant.MultiKeyModeRandom}}, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := channelRetryCapacity(test.channel); got != test.want {
				t.Fatalf("capacity=%d want=%d", got, test.want)
			}
		})
	}
}

func TestProtocolAttemptWarningIsScopedToCurrentAttempt(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	setProtocolAttemptWarning(c, &textgen.RelayPlan{Warnings: []textgen.Issue{{Message: "stateless_bridge"}}})
	if got := recorder.Header().Get("X-Lazu-Warning"); got != "stateless_bridge" {
		t.Fatalf("warning=%q", got)
	}
	setProtocolAttemptWarning(c, &textgen.RelayPlan{Native: true})
	if got := recorder.Header().Get("X-Lazu-Warning"); got != "" {
		t.Fatalf("stale warning survived native retry: %q", got)
	}
	common.SetContextKey(c, constant.ContextKeyOSSURLApplied, true)
	if shouldApplyOSSURL(c) {
		t.Fatal("controller must not apply OSS enrichment twice")
	}
}
