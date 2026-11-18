package openai

import (
	"bytes"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

func TestNativeResponsesBodyPreservesUntouchedBytes(t *testing.T) {
	body := []byte("{ \"model\" : \"gpt-4.1\", \"input\":\"hello \\u4e16\\u754c\", \"vendor_number\":9007199254740993, \"vendor_escape\":\"a\\/b\" }\n")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-4.1"}}
	got, err := (&Adaptor{}).ConvertNativeRequest(body, info, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, got) {
		t.Fatalf("native byte identity lost: %s", got)
	}
}
func TestNativeRequestSurgicalPolicyDoesNotChangeOtherFields(t *testing.T) {
	input := []byte(`{"model":"gpt-5-high","input":"hello","metadata":{"lazu":{"session":"secret"},"caller":"ok"},"store":false,"service_tier":"priority","vendor_number":9007199254740993,"stream_options":{"include_obfuscation":false,"caller":true}}`)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5"}}
	got, err := (&Adaptor{}).ConvertNativeRequest(input, info, true)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got, "metadata.lazu").Exists() || gjson.GetBytes(got, "service_tier").Exists() || gjson.GetBytes(got, "stream_options.include_obfuscation").Exists() {
		t.Fatalf("private/disabled fields leaked: %s", got)
	}
	if gjson.GetBytes(got, "vendor_number").Raw != "9007199254740993" || gjson.GetBytes(got, "metadata.caller").Str != "ok" || gjson.GetBytes(got, "store").Type != gjson.False {
		t.Fatalf("unrelated fields changed: %s", got)
	}
	if gjson.GetBytes(input, "model").Str != "gpt-5-high" || !gjson.GetBytes(input, "metadata.lazu").Exists() {
		t.Fatal("mutated original bytes")
	}
}
func TestNativeChatKeepsExistingGPT5PolicyAndStreamUsage(t *testing.T) {
	input := []byte(`{"model":"gpt-5","stream":true,"temperature":0.4,"max_tokens":4096,"messages":[{"role":"system","content":"hello"}],"stream_options":{"include_usage":false,"vendor":true}}`)
	previous := constant.ForceStreamOption
	constant.ForceStreamOption = true
	defer func() { constant.ForceStreamOption = previous }()
	info := &relaycommon.RelayInfo{IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-5", SupportStreamOptions: true}}
	got, err := (&Adaptor{}).ConvertNativeRequest(input, info, false)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got, "max_tokens").Exists() || gjson.GetBytes(got, "temperature").Exists() || gjson.GetBytes(got, "max_completion_tokens").Int() != 4096 || gjson.GetBytes(got, "messages.0.role").Str != "developer" || !gjson.GetBytes(got, "stream_options.include_usage").Bool() {
		t.Fatalf("chat conversion policy lost: %s", got)
	}
	if !gjson.GetBytes(got, "stream_options.vendor").Bool() {
		t.Fatal("unknown stream extension lost")
	}
}

func TestNativeGPT6ChatToolsReasoningDefaultPreservesCallerFields(t *testing.T) {
	for _, tc := range []struct {
		name, model, suffix, dialect string
		responses                    bool
		wantDefault                  bool
	}{
		{"default", "gpt-6-sol", `}`, "openai", false, true},
		{"gpt-6.1-terra default", "gpt-6.1-terra", `}`, "openai", false, true},
		{"gpt-6.1-sol untouched", "gpt-6.1-sol", `}`, "openai", false, false},
		{"gpt-6.2-sol untouched", "gpt-6.2-sol", `}`, "openai", false, false},
		{"gpt-6-astra untouched", "gpt-6-astra", `}`, "openai", false, false},
		{"client effort kept", "gpt-6-sol", `, "reasoning_effort":"high"}`, "openai", false, false},
		{"explicit reasoning kept", "gpt-6-sol", `, "reasoning":{"effort":"high"}}`, "openai", false, false},
		{"tools disabled", "gpt-6-sol", `, "tool_choice":"none"}`, "openai", false, false},
		{"reseller untouched", "gpt-6-sol", `}`, "openai_chat_compatible", false, false},
		{"responses untouched", "gpt-6-sol", `}`, "openai", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := `{ "model" : "` + tc.model + `", "messages":[{"role":"user","content":"weather?"}], "tools":[{"type":"function","function":{"name":"weather"}}], "vendor_number":9007199254740993, "vendor_escape":"a\/b"`
			input := []byte(base + tc.suffix)
			original := bytes.Clone(input)
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: tc.model},
				ProtocolPlan: &textgen.RelayPlan{
					Native:           true,
					UpstreamProtocol: textgen.Protocol{Dialect: tc.dialect},
				},
			}
			got, err := (&Adaptor{}).ConvertNativeRequest(input, info, tc.responses)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantDefault {
				if gjson.GetBytes(got, "reasoning_effort").Str != "none" {
					t.Fatalf("reasoning default missing: %s", got)
				}
				for _, field := range []string{"messages", "tools", "vendor_number", "vendor_escape"} {
					if gjson.GetBytes(got, field).Raw != gjson.GetBytes(original, field).Raw {
						t.Fatalf("changed caller field %s: %s", field, got)
					}
				}
			} else if !bytes.Equal(got, original) {
				t.Fatalf("untouched request lost byte identity: %s", got)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("mutated original native body")
			}
		})
	}
}
