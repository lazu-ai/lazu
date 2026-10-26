package openai

import (
	"bytes"
	"strconv"
	"strings"
	"unsafe"

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
	// every sampling field left out of the body, said once to the client at
	// the end (relaycommon.WarnSamplingRefused)
	var dropped []string
	// Most lookups below ask for a top-level key the request does not carry,
	// and gjson answers "absent" only after scanning the whole body. The index
	// scans once per body version; every edit starts a new version.
	index := newWireIndex(body)
	get := func(path string) gjson.Result { return index.get(path) }
	set := func(path string, value interface{}) {
		if err != nil {
			return
		}
		existing := get(path)
		if existing.Exists() && existing.Value() == value {
			return
		}
		body, err = sjson.SetBytes(body, path, value)
		index.reset(body)
	}
	del := func(path string) {
		for err == nil && get(path).Exists() {
			body, err = sjson.DeleteBytes(body, path)
			index.reset(body)
		}
	}
	if info.UpstreamModelName != "" && get("model").Str != info.UpstreamModelName {
		set("model", info.UpstreamModelName)
	}
	del("metadata.lazu")
	if metadata := get("metadata"); metadata.IsObject() && len(metadata.Map()) == 0 {
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
			common.ChatToolsNeedReasoningOff(model) && get("reasoning_effort").Str == "" &&
			!get("reasoning").Exists() && get("tools.#").Int() > 0 &&
			get("tool_choice").Str != "none" {
			set("reasoning_effort", "none")
		}
		if strings.HasPrefix(model, "o") || strings.HasPrefix(model, "gpt-5") {
			if get("max_completion_tokens").Uint() == 0 && get("max_tokens").Uint() != 0 {
				set("max_completion_tokens", get("max_tokens").Value())
				del("max_tokens")
			}
			if strings.HasPrefix(model, "o") || model != "gpt-5-chat-latest" {
				if get("temperature").Exists() {
					dropped = append(dropped, "temperature")
				}
				del("temperature")
			}
			if !strings.HasPrefix(model, "o1-mini") && !strings.HasPrefix(model, "o1-preview") && get("messages.0.role").Str == "system" {
				set("messages.0.role", "developer")
			}
			info.ReasoningEffort = strings.Clone(get("reasoning_effort").Str)
		}
		// Thought signatures only belong on Gemini upstreams. Iterate just the
		// tool-call container; the message content itself is never decoded again.
		if bytes.Contains(body, []byte("extra_content")) {
			for i, message := range get("messages").Array() {
				for j, call := range message.Get("tool_calls").Array() {
					if call.Get("extra_content").Exists() {
						del("messages." + strconv.Itoa(i) + ".tool_calls." + strconv.Itoa(j) + ".extra_content")
					}
				}
			}
		}
	}
	if get("temperature").Exists() || get("top_p").Exists() {
		effort := get("reasoning_effort").Str
		if effort == "" {
			effort = get("reasoning.effort").Str
		}
		for _, field := range samplingRefusedWhileReasoning(info, effort) {
			if get(field).Exists() {
				dropped = append(dropped, field)
			}
			del(field)
		}
	}
	// no gin context here: the plan carries the warning and the caller
	// rewrites the header (relaycommon.SetProtocolWarningHeader)
	relaycommon.WarnSamplingRefused(nil, info, dropped)
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

// wireIndex answers gjson paths against a JSON object body from one scan of
// its top-level keys. A path's first segment selects the field (the first
// occurrence, as gjson.GetBytes would); the rest is looked up inside that
// field's raw value only.
type wireIndex struct {
	body   []byte
	fields map[string]gjson.Result
	object bool
}

func newWireIndex(body []byte) *wireIndex { return &wireIndex{body: body} }

func (x *wireIndex) reset(body []byte) {
	x.body, x.fields = body, nil
}

func (x *wireIndex) get(path string) gjson.Result {
	if x.fields == nil {
		x.fields = map[string]gjson.Result{}
		// A view, not a copy (gjson.ParseBytes copies): results are read only
		// while this body version is current, and anything kept beyond that
		// is cloned by the caller.
		root := gjson.Parse(unsafe.String(unsafe.SliceData(x.body), len(x.body)))
		x.object = root.IsObject()
		if x.object {
			root.ForEach(func(key, value gjson.Result) bool {
				if _, seen := x.fields[key.Str]; !seen {
					x.fields[key.Str] = value
				}
				return true
			})
		}
	}
	first, rest, nested := strings.Cut(path, ".")
	if !x.object || strings.ContainsAny(first, `*?\|#@!`) {
		return gjson.GetBytes(x.body, path)
	}
	field, ok := x.fields[first]
	if !ok {
		return gjson.Result{}
	}
	if !nested {
		return field
	}
	return field.Get(rest)
}
