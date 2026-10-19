package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/types"
)

// A mistyped endpoint_type used to fall through to /v1/chat/completions, which
// made image models look broken when the test itself was wrong.
func TestTestChannelRejectsUnknownEndpointType(t *testing.T) {
	ch := &model.Channel{Type: 1}
	result := testChannel(ch, "gpt-image-2", "image_generation") // underscore, not the real "image-generation"

	if result.localErr == nil {
		t.Fatal("expected unknown endpoint_type to error instead of silently using chat completions")
	}
	if !strings.Contains(result.localErr.Error(), "unknown endpoint_type") {
		t.Fatalf("expected unknown endpoint_type error, got %v", result.localErr)
	}
}

func TestMarshalChannelTestRequestHonorsExplicitStreamRequirement(t *testing.T) {
	for _, stream := range []bool{false, true} {
		info := &relaycommon.RelayInfo{
			IsStream:  stream,
			RelayMode: relayconstant.RelayModeChatCompletions,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{
				ExplicitStreamRequired: true,
			}},
		}
		body, err := marshalChannelTestRequest(map[string]any{"model": "kimi-k2.6"}, info)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		value, ok := decoded["stream"].(bool)
		if !ok || value != stream {
			t.Fatalf("stream=%v present=%v, want stream=%v", value, ok, stream)
		}
	}
}

func TestMarshalChannelTestRequestDoesNotAddStreamToNonStreamingEndpoints(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeEmbeddings,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{
			ExplicitStreamRequired: true,
		}},
	}
	body, err := marshalChannelTestRequest(map[string]any{"model": "embedding-model"}, info)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded["stream"]; exists {
		t.Fatalf("non-streaming endpoint received stream field: %s", body)
	}
}

func TestMarshalChannelTestRequestHonorsExplicitStreamForClaude(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		RelayMode:   relayconstant.RelayModeUnknown,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{
			ExplicitStreamRequired: true,
		}},
	}
	body, err := marshalChannelTestRequest(map[string]any{"model": "claude-test"}, info)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	value, ok := decoded["stream"].(bool)
	if !ok || value {
		t.Fatalf("Claude probe stream=%v present=%v, want explicit false", value, ok)
	}
}
