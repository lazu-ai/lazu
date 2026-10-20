package fixtures

import (
	"strings"
	"testing"
)

func TestRecordingsLoad(t *testing.T) {
	chat := Data(t, "deepseek/chat_text.sse")
	if len(chat) < 3 || !strings.HasPrefix(chat[0], "{") || chat[len(chat)-1] != "[DONE]" {
		t.Fatalf("deepseek chat recording: %d events, first %q, last %q", len(chat), chat[0], chat[len(chat)-1])
	}
	claude := Events(t, "anthropic/thinking_block.sse")
	if len(claude) < 5 || claude[0].Name != "message_start" || claude[len(claude)-1].Name != "message_stop" {
		t.Fatalf("anthropic recording: %d events, first %q, last %q", len(claude), claude[0].Name, claude[len(claude)-1].Name)
	}
	refusal := Events(t, "deepseek/chat_json_schema_refused.json")
	if len(refusal) != 1 || !strings.Contains(refusal[0].Data, "response_format") {
		t.Fatalf("refusal recording: %+v", refusal)
	}
}
