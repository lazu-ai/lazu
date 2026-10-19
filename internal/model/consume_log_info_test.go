package model

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
)

func TestConsumeInfoIsCompactWhileDurableFactsRemainComplete(t *testing.T) {
	withRelayResultQueueTestFlags(t)
	previousEnabled, previousLevel, previousWriter := common.LogConsumeEnabled, common.LogLevel, gin.DefaultWriter
	common.LogConsumeEnabled, common.LogLevel = true, common.LogLevelInfo
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() {
		common.LogConsumeEnabled, common.LogLevel, gin.DefaultWriter = previousEnabled, previousLevel, previousWriter
	})
	for _, enabled := range []bool{true, false} {
		output.Reset()
		if enabled {
			common.LogLevel = common.LogLevelInfo
		} else {
			common.LogLevel = common.LogLevelWarn
		}
		c := newRelayResultQueueTestContext()
		common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{})
		MarkRelayResultFlushExpected(c)
		RecordConsumeLog(c, 7, RecordConsumeLogParams{ModelName: "gpt-4o", AmountMicrousd: 42, Content: "private-content-marker", LineItems: "complete-price-facts", Other: map[string]interface{}{"private_diagnostic": "private-other-marker"}})
		value, found := c.Get(relayResultConsumeLogContextKey)
		if !found {
			t.Fatal("compact logging lost durable consume event")
		}
		event := value.(relayConsumeLogEvent)
		if event.Content != "private-content-marker" || event.LineItems != "complete-price-facts" || !strings.Contains(event.Other, "private-other-marker") || event.AmountMicrousd != 42 {
			t.Fatalf("incomplete durable facts: %+v", event)
		}
		if enabled {
			if !strings.Contains(output.String(), "userId=7 model=gpt-4o amount_microusd=42") {
				t.Fatalf("missing compact identity/cost: %q", output.String())
			}
			if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "complete-price-facts") || output.Len() > 200 {
				t.Fatalf("INFO duplicated full facts: %q", output.String())
			}
		} else if output.Len() != 0 {
			t.Fatalf("disabled INFO produced output: %q", output.String())
		}
	}
}
