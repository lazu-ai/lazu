package relay

import (
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

func TestResponsesToChatBridgeStripsPrivateLazuMetadata(t *testing.T) {
	semantic := &textgen.Request{
		Model: textgen.ModelRef{Upstream: "gpt-4o-mini"},
		Conversation: []textgen.InputItem{{
			Kind:    textgen.InputMessage,
			Role:    "user",
			Content: []textgen.ContentPart{{Kind: textgen.ContentText, Text: "hello"}},
		}},
		Metadata: textgen.MetadataOptions{Metadata: json.RawMessage(`{"trace_id":"public","lazu":{"intent":"code"}}`)},
	}
	stripLazuMetadataFromSemantic(semantic)
	wire, err := openai_chat.Encode(semantic)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(wire.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if _, exists := metadata["lazu"]; exists {
		t.Fatalf("private routing metadata leaked into Chat request: %#v", metadata)
	}
	if metadata["trace_id"] != "public" {
		t.Fatalf("caller metadata was not preserved: %#v", metadata)
	}
}

func TestResponsesOutboundHonorsExplicitStreamRequirement(t *testing.T) {
	for _, stream := range []bool{false, true} {
		info := &relaycommon.RelayInfo{
			IsStream: stream,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{
				ExplicitStreamRequired: true,
			}},
		}
		body, err := applyExplicitStreamRequirement([]byte(`{"model":"kimi-k2.6"}`), info)
		if err != nil {
			t.Fatal(err)
		}
		value := gjson.GetBytes(body, "stream")
		if !value.Exists() || value.Bool() != stream {
			t.Fatalf("stream=%v exists=%v, want stream=%v", value.Bool(), value.Exists(), stream)
		}
	}
}
