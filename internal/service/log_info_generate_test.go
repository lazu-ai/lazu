package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestTextOtherInfoTimingIsOnlyRecordedForStreamsThatAnswered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	start := time.Now().Add(-3 * time.Second)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	plain := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, StartTime: start, FirstResponseTime: start.Add(-time.Second), FinishReason: "stop"}
	other := GenerateTextOtherInfo(c, plain, 0, 0)
	for _, key := range []string{"frt", "ttft_ms", "max_stall_ms"} {
		if _, ok := other[key]; ok {
			t.Fatalf("non-streaming row carries %s: %v", key, other)
		}
	}
	if other["finish_reason"] != "stop" {
		t.Fatalf("finish reason lost: %v", other)
	}

	streamed := &relaycommon.RelayInfo{
		ChannelMeta:       &relaycommon.ChannelMeta{},
		StartTime:         start,
		FirstResponseTime: start.Add(200 * time.Millisecond),
		FirstOutputTime:   start.Add(900 * time.Millisecond),
		MaxStallMs:        1200,
		IsStream:          true,
	}
	other = GenerateTextOtherInfo(c, streamed, 0, 0)
	if other["frt"] != float64(200) || other["ttft_ms"] != int64(900) || other["max_stall_ms"] != 1200 {
		t.Fatalf("stream timing wrong: frt=%v ttft=%v stall=%v", other["frt"], other["ttft_ms"], other["max_stall_ms"])
	}
}

func TestTextOtherInfoTellsRetriesFromChannelSwitches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, StartTime: time.Now()}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("use_channel", []string{"7", "7", "7"})
	other := GenerateTextOtherInfo(c, info, 0, 0)
	if other["attempts"] != 3 || other["channels_tried"] != 1 {
		t.Fatalf("same-channel retries: %v", other)
	}

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Set("use_channel", []string{"7", "9"})
	other = GenerateTextOtherInfo(c, info, 0, 0)
	if other["attempts"] != 2 || other["channels_tried"] != 2 {
		t.Fatalf("fallback: %v", other)
	}

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Set("use_channel", []string{"7"})
	other = GenerateTextOtherInfo(c, info, 0, 0)
	if _, ok := other["attempts"]; ok {
		t.Fatalf("a single attempt needs no count: %v", other)
	}
}
