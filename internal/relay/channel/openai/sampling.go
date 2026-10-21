package openai

import (
	"strings"

	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

// samplingFields are the request fields a reasoning model may refuse while
// it reasons. Only these are ever dropped, and only when the catalog row
// lists them in unsupported_params.
var samplingFields = []string{"temperature", "top_p"}

// describeModel is the catalog lookup; tests replace it.
var describeModel = textgenruntime.Descriptor

// samplingRefusedWhileReasoning names the sampling fields to leave out of a
// request to this model: those its catalog row lists in unsupported_params,
// unless the request turns reasoning off. OpenAI's reasoning models refuse
// temperature and top_p with a 400 while they reason and take them at effort
// "none" (gpt-6-luna and gpt-6-sol in production, 2026-10-21). effort is the
// one the request will be sent with, "" when it names none: the model's
// default effort is not "none" for every model, so an unnamed one counts as
// reasoning. The row is looked up on the upstream name, then the client's.
func samplingRefusedWhileReasoning(info *relaycommon.RelayInfo, effort string) []string {
	if info == nil || strings.EqualFold(effort, "none") {
		return nil
	}
	var names []string
	if info.ChannelMeta != nil {
		names = append(names, info.UpstreamModelName)
	}
	names = append(names, info.OriginModelName)
	for _, name := range names {
		if name == "" {
			continue
		}
		descriptor := describeModel(name)
		if !descriptor.Found {
			continue
		}
		var refused []string
		for _, field := range samplingFields {
			for _, param := range descriptor.UnsupportedParams {
				if strings.EqualFold(strings.TrimSpace(param), field) {
					refused = append(refused, field)
					break
				}
			}
		}
		return refused
	}
	return nil
}
