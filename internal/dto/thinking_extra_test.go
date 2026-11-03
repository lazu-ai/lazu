package dto

import (
	"encoding/json"
	"testing"
)

// Keys Anthropic adds to thinking survive the decode and re-encode the
// native path does; modeled keys keep their struct values.
func TestThinkingKeepsUnmodeledKeys(t *testing.T) {
	var request ClaudeRequest
	if err := json.Unmarshal([]byte(`{"model":"m","thinking":{"type":"adaptive","display":"omitted","block_binding":{"account":"acct_1"}}}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Thinking == nil || request.Thinking.Type != "adaptive" || string(request.Thinking.Extra["block_binding"]) != `{"account":"acct_1"}` {
		t.Fatalf("decoded %+v", request.Thinking)
	}
	request.Thinking.Display = "summarized"
	out, err := json.Marshal(request.Thinking)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"block_binding":{"account":"acct_1"},"display":"summarized","type":"adaptive"}` {
		t.Fatalf("encoded %s", out)
	}
	plain, _ := json.Marshal(&Thinking{Type: "enabled", BudgetTokens: new(int)})
	if string(plain) != `{"type":"enabled","budget_tokens":0}` {
		t.Fatalf("plain %s", plain)
	}
}
