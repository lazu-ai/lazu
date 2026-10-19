package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestClaudeToOpenAIRequest_PreserveHashThinkingSuffix(t *testing.T) {
	claudeRequest := dto.ClaudeRequest{
		Model: "claude-sonnet-4-20250514",
		Thinking: &dto.Thinking{
			Type:         "enabled",
			BudgetTokens: common.GetPointer(2048),
		},
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-sonnet-4-20250514#thinking",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI,
		},
	}

	openAIRequest, err := ClaudeToOpenAIRequest(claudeRequest, info)
	if err != nil {
		t.Fatalf("ClaudeToOpenAIRequest failed: %v", err)
	}
	if openAIRequest.Model != "claude-sonnet-4-20250514#thinking" {
		t.Fatalf("expected model suffix #thinking to be preserved, got %q", openAIRequest.Model)
	}
}
