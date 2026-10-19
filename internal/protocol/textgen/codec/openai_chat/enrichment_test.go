package openai_chat

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
)

func TestOSSURLHintIsPrivateAndEnrichedWireCanBeSynced(t *testing.T) {
	request, wire, err := Decode([]byte(`{"model":"kimi-k2.6","messages":[{"role":"user","content":"describe"}],"OSS_URL":"https://example.test/a.png"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !HasOSSURLHint(request) || len(request.Raw.UnknownFields) != 0 {
		t.Fatalf("OSS_URL should be a recognized private gateway hint: %#v", request.Raw)
	}
	wire.Messages[0].SetMediaContent([]dto.MediaContent{{Type: dto.ContentTypeText, Text: "describe"}, {Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: "data:image/png;base64,abc"}}})
	effective := request.Clone()
	if err := SyncEnrichedWire(effective, wire); err != nil {
		t.Fatalf("SyncEnrichedWire: %v", err)
	}
	if len(effective.Conversation) != 1 || len(effective.Conversation[0].Content) != 2 || effective.Conversation[0].Content[1].Kind != textgen.ContentImage {
		t.Fatalf("enriched media was not reflected in semantic request: %#v", effective.Conversation)
	}
	if len(request.Conversation[0].Content) != 1 {
		t.Fatal("original immutable semantic request was mutated")
	}
}
