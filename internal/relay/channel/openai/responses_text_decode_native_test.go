package openai

import (
	"bytes"
	"reflect"
	"testing"

	responses "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_responses"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestResponsesCommonTextDecodePreservesNativeBody(t *testing.T) {
	for _, input := range []string{
		`[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello \\u4e16\\u754c"}]}]`,
		`[{"\u0072ole":"user","\u0063ontent":[{"\u0074ype":"input_text","\u0074ext":"\u4e16\u754c\n\"a\\b\"\/\ud83d\ude03"}]}]`,
		`[{"role":"assistant","content":[]}]`,
		`[{"role":"assistant","role":null,"content":[{"type":"input_text","text":"first","text":null}]}]`,
		`[{"role":"user","content":[{"type":"input_image","file_id":"file-image"}],"future_item":{"v":1}}]`,
		`[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"a\":1}"}]`,
	} {
		body := []byte("{ \"model\" : \"gpt-mock\", \"input\" : " + input + `, "metadata":{"trace":"synthetic"}, "tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false}], "vendor_number":9007199254740993, "vendor_escape":"a\/b" }` + "\n")
		original := bytes.Clone(body)
		request, wire, err := responses.Decode(body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(wire.Input, []byte(input)) || !bytes.Equal(request.Raw.TopLevel["input"], []byte(input)) {
			t.Fatal("retained native input changed")
		}
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-mock"}}
		outbound, err := (&Adaptor{}).ConvertNativeRequest(body, info, true)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(outbound, original) || !bytes.Equal(body, original) {
			t.Fatal("native bytes, escaping, whitespace or precision changed")
		}
		again, _, err := responses.Decode(outbound)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(again, request) {
			t.Fatal("native forwarding changed the IR")
		}
	}
}
