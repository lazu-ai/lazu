package openai

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/wirepatch"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertNativeRequest applies the same channel policy as DTO conversion using
// surgical wire edits. Untouched requests retain whitespace, escaping, unknown
// fields and numeric precision exactly as supplied by the client.
func (a *Adaptor) ConvertNativeRequest(body []byte, info *relaycommon.RelayInfo, responses bool) ([]byte, error) {
	var err error
	set := func(path string, value interface{}) {
		if err != nil {
			return
		}
		existing := gjson.GetBytes(body, path)
		if existing.Exists() && existing.Value() == value {
			return
		}
		body, err = sjson.SetBytes(body, path, value)
	}
	del := func(path string) {
		for err == nil && gjson.GetBytes(body, path).Exists() {
			body, err = sjson.DeleteBytes(body, path)
		}
	}
	if info.UpstreamModelName != "" && gjson.GetBytes(body, "model").Str != info.UpstreamModelName {
		set("model", info.UpstreamModelName)
	}
	del("metadata.lazu")
	if metadata := gjson.GetBytes(body, "metadata"); metadata.IsObject() && len(metadata.Map()) == 0 {
		del("metadata")
	}
	if !responses {
		if info.ChannelType != constant.ChannelTypeOpenAI || !info.SupportStreamOptions || !info.IsStream {
			del("stream_options")
		} else if constant.ForceStreamOption {
			set("stream_options.include_usage", true)
		}
		model := info.UpstreamModelName
		if info.ProtocolPlan != nil && info.ProtocolPlan.Native && info.ProtocolPlan.UpstreamProtocol.Dialect == "openai" &&
			common.ChatToolsNeedReasoningOff(model) && gjson.GetBytes(body, "reasoning_effort").Str == "" &&
			!gjson.GetBytes(body, "reasoning").Exists() && gjson.GetBytes(body, "tools.#").Int() > 0 &&
			gjson.GetBytes(body, "tool_choice").Str != "none" {
			set("reasoning_effort", "none")
		}
		if strings.HasPrefix(model, "o") || strings.HasPrefix(model, "gpt-5") {
			if gjson.GetBytes(body, "max_completion_tokens").Uint() == 0 && gjson.GetBytes(body, "max_tokens").Uint() != 0 {
				set("max_completion_tokens", gjson.GetBytes(body, "max_tokens").Value())
				del("max_tokens")
			}
			if strings.HasPrefix(model, "o") || model != "gpt-5-chat-latest" {
				del("temperature")
			}
			if !strings.HasPrefix(model, "o1-mini") && !strings.HasPrefix(model, "o1-preview") && gjson.GetBytes(body, "messages.0.role").Str == "system" {
				set("messages.0.role", "developer")
			}
			info.ReasoningEffort = gjson.GetBytes(body, "reasoning_effort").Str
		}
		// Thought signatures only belong on Gemini upstreams. Iterate just the
		// tool-call container; the message content itself is never decoded again.
		if bytes.Contains(body, []byte("extra_content")) {
			for i, message := range gjson.GetBytes(body, "messages").Array() {
				for j, call := range message.Get("tool_calls").Array() {
					if call.Get("extra_content").Exists() {
						del("messages." + strconv.Itoa(i) + ".tool_calls." + strconv.Itoa(j) + ".extra_content")
					}
				}
			}
		}
	}
	if gjson.GetBytes(body, "temperature").Exists() || gjson.GetBytes(body, "top_p").Exists() {
		effort := gjson.GetBytes(body, "reasoning_effort").Str
		if effort == "" {
			effort = gjson.GetBytes(body, "reasoning.effort").Str
		}
		var dropped []string
		for _, field := range samplingRefusedWhileReasoning(info, effort) {
			if gjson.GetBytes(body, field).Exists() {
				dropped = append(dropped, field)
			}
			del(field)
		}
		// no gin context here: the plan carries the warning and the caller
		// rewrites the header (relaycommon.SetProtocolWarningHeader)
		relaycommon.WarnSamplingRefused(nil, info, dropped)
	}
	if err != nil {
		return nil, err
	}
	body, err = relaycommon.RemoveDisabledFields(body, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, err
	}
	if len(info.ParamOverride) > 0 {
		body, err = relaycommon.ApplyParamOverride(body, info.ParamOverride)
		if err != nil {
			return nil, err
		}
	}
	// wire dialect patch: vendor quirks on the compiled body, once per
	// attempt, after ParamOverride (internal/relay/wirepatch).
	return wirepatch.Apply(wirepatch.ResolveDialect(info), body), nil
}
