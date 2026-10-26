package service

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

// A Gemini client may send up to five stop sequences. The legacy converter
// sliced them to four (OpenAI's limit) and panicked on fewer; the hub sends
// them all and the upstream judges them, as it does a Chat client's.
func TestGeminiToChatCarriesEveryStopSequence(t *testing.T) {
	for _, stops := range [][]string{{"END"}, {"A", "B", "C", "D", "E"}} {
		wire := dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hi"}}}}}
		wire.GenerationConfig.StopSequences = stops
		body, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		chat, err := geminiToChat(body)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(chat.Stop)
		var got []string
		_ = json.Unmarshal(raw, &got)
		if len(got) != len(stops) {
			t.Fatalf("stops %v became %v", stops, chat.Stop)
		}
	}
}
