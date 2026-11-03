package claude

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestApplyAdaptiveThinkingKeepsUnmodeledKeys(t *testing.T) {
	budget := 8192
	request := &dto.ClaudeRequest{Thinking: &dto.Thinking{Type: "enabled", BudgetTokens: &budget,
		Extra: map[string]json.RawMessage{"block_binding": json.RawMessage(`{"account":"acct_1"}`)}}}
	ApplyAdaptiveThinking(request, "high")
	if request.Thinking.Type != "adaptive" || request.Thinking.BudgetTokens != nil || string(request.Thinking.Extra["block_binding"]) != `{"account":"acct_1"}` {
		t.Fatalf("thinking = %+v", request.Thinking)
	}
}
