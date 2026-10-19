package openai_responses

import (
	"encoding/json"
	"strings"

	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

// Only the gateway's stateless plaintext shape has a Chat history equivalent.
// Never drop encrypted state, summaries, extensions or incomplete reasoning.
// Historical gateway items had no summary field; native Responses items do.
func decodeGatewayReasoning(item map[string]json.RawMessage) textgen.Option[string] {
	var absent textgen.Option[string]
	for key := range item {
		switch key {
		case "type", "id", "status", "summary", "content":
		default:
			return absent
		}
	}
	var id, status string
	if json.Unmarshal(item["id"], &id) != nil || !strings.HasPrefix(id, "rs_") || len(id) <= 3 || json.Unmarshal(item["status"], &status) != nil || status != "completed" {
		return absent
	}
	if summary, present := item["summary"]; present {
		var values []json.RawMessage
		if !strings.HasPrefix(id, "rs_lazu_") || json.Unmarshal(summary, &values) != nil || values == nil || len(values) != 0 {
			return absent
		}
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(item["content"], &parts) != nil || len(parts) != 1 || len(parts[0]) != 2 {
		return absent
	}
	var kind, text string
	if json.Unmarshal(parts[0]["type"], &kind) != nil || kind != "reasoning_text" || json.Unmarshal(parts[0]["text"], &text) != nil || text == "" {
		return absent
	}
	return textgen.Option[string]{Set: true, Value: text}
}
