package relay

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	openai_responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestSingleUserTextResponsesNormalization(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{ResponsesSingleUserText: true}},
		ProtocolPlan: &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
	}
	for _, input := range []string{`"  hello\n世界  "`, `[{"role":"user","content":"  hello\n世界  "}]`, `[{"role":"user","content":[{"type":"input_text","text":"  hello\n世界  "}]}]`} {
		body := []byte(`{"model":"sol","input":` + input + `,"store":false,"stream":true,"temperature":0,"max_output_tokens":256}`)
		got, err := normalizeSingleUserTextResponses(body, info)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(got, &fields); err != nil {
			t.Fatal(err)
		}
		var text string
		if err := json.Unmarshal(fields["input"], &text); err != nil || text != "  hello\n世界  " {
			t.Fatalf("text was changed: %q (%v)", text, err)
		}
		for key, want := range map[string]string{"store": "false", "stream": "true", "temperature": "0", "max_output_tokens": "256"} {
			if string(fields[key]) != want {
				t.Fatalf("%s changed: %s", key, fields[key])
			}
		}
	}
	for _, body := range []string{
		`{"model":"sol","input":null}`,
		`{"model":"sol","input":[{"role":"user","content":"one"},{"role":"user","content":"two"}]}`,
		`{"model":"sol","input":[{"role":"assistant","content":"hello"}]}`,
		`{"model":"sol","input":"hello","instructions":"system"}`,
		`{"model":"sol","input":"hello","tools":[{"type":"web_search_preview"}]}`,
		`{"model":"sol","input":"hello","store":true}`,
		`{"model":"sol","input":"hello","previous_response_id":"resp_previous"}`,
		`{"model":"sol","input":"hello","conversation":"conv_previous"}`,
		`{"model":"sol","input":"hello","prompt":{"id":"template"}}`,
		`{"model":"sol","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/a.png"}]}]}`,
		`{"model":"sol","input":[{"role":"user","content":"hello","future_field":true}]}`,
	} {
		if _, err := normalizeSingleUserTextResponses([]byte(body), info); err == nil {
			t.Fatalf("unsafe input accepted: %s", body)
		}
	}
	info.ChannelOtherSettings.ResponsesSingleUserText = false
	body := []byte(`{"model":"other","input":[{"role":"assistant","content":"unchanged"}]}`)
	got, err := normalizeSingleUserTextResponses(body, info)
	if err != nil || !bytes.Equal(body, got) {
		t.Fatal("unconfigured channel was changed")
	}
}

func TestSingleUserTextChatBridgePreservesStreamAndTokenLimit(t *testing.T) {
	semantic, _, err := openai_chat.Decode([]byte(`{"model":"sol","messages":[{"role":"user","content":"OK"}],"stream":true,"max_completion_tokens":128}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := textgen.SingleUserText(semantic); err != nil {
		t.Fatal(err)
	}
	wire, err := openai_responses.Encode(semantic)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{ResponsesSingleUserText: true}},
		ProtocolPlan: &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolOpenAIResponses}},
	}
	body, err = normalizeSingleUserTextResponses(body, info)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Input     string `json:"input"`
		Stream    bool   `json:"stream"`
		MaxOutput int    `json:"max_output_tokens"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Input != "OK" || !result.Stream || result.MaxOutput != 128 {
		t.Fatalf("Chat bridge lost request semantics: %s (%v)", body, err)
	}
}
