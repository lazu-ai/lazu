package service

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// A Gemini client may send up to five stop sequences; Chat takes four. The
// legacy converter sliced them to four and panicked on fewer.
func TestGeminiToOpenAIRequestKeepsFewerThanFourStopSequences(t *testing.T) {
	for _, stops := range [][]string{{"END"}, {"A", "B", "C", "D", "E"}} {
		wire := dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hi"}}}}}
		wire.GenerationConfig.StopSequences = stops
		chat, err := GeminiToOpenAIRequest(&wire, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := chat.Stop.([]string)
		want := min(len(stops), 4)
		if len(got) != want {
			t.Fatalf("stops %v became %v", stops, chat.Stop)
		}
	}
}
