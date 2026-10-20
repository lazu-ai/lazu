package wirepatch

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/packages/domain/providercap"
)

// Every registered quirk is named, justified and sourced (AGENTS.md Protocol
// Layer rule 6), applies to at least one dialect, and has an ID no other
// patch uses.
func TestRegistryEntriesAreNamedJustifiedAndSourced(t *testing.T) {
	patches := Patches()
	if len(patches) == 0 {
		t.Fatal("registry is empty")
	}
	seen := map[string]bool{}
	for _, p := range patches {
		if p.ID == "" || strings.ToLower(p.ID) != p.ID || strings.ContainsAny(p.ID, " -") {
			t.Errorf("patch ID %q must be non-empty snake_case", p.ID)
		}
		if seen[p.ID] {
			t.Errorf("duplicate patch ID %q", p.ID)
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Reason) == "" {
			t.Errorf("%s: empty reason", p.ID)
		}
		if !strings.HasPrefix(p.Source, "https://") {
			t.Errorf("%s: source must be a link, got %q", p.ID, p.Source)
		}
		if len(p.Dialects) == 0 {
			t.Errorf("%s: applies to no dialect", p.ID)
		}
		// SnapshotForChannel also emits "xai" outside NormalizeDialect; a patch
		// for that dialect would need this guard widened.
		for _, d := range p.Dialects {
			if providercap.NormalizeDialect(constant.APITypeOpenAI, d) != d &&
				providercap.NormalizeDialect(constant.APITypeAnthropic, d) != d &&
				providercap.NormalizeDialect(constant.APITypeGemini, d) != d {
				t.Errorf("%s: dialect %q is not one NormalizeDialect can produce, so the patch would never run", p.ID, d)
			}
		}
		if p.Apply == nil {
			t.Errorf("%s: nil Apply", p.ID)
		}
	}
}

func TestApplyLeavesOtherDialectsAndEmptyBodiesAlone(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","function":{"parameters":{"properties":{"a":{"pattern":"\\0"}}}}}]}`)
	for _, dialect := range []string{"", providercap.DialectOpenAI, providercap.DialectOpenAIChatCompatible, providercap.DialectAnthropic} {
		if got := Apply(dialect, body); string(got) != string(body) {
			t.Errorf("dialect %q changed the body: %s", dialect, got)
		}
	}
	if got := Apply(providercap.DialectDeepSeek, nil); got != nil {
		t.Errorf("nil body became %q", got)
	}
	if got := Apply(providercap.DialectDeepSeek, body); !strings.Contains(string(got), `\\u0000`) {
		t.Errorf("deepseek dialect did not run its patch: %s", got)
	}
}

func TestResolveDialectPrefersPlanThenChannel(t *testing.T) {
	if got := ResolveDialect(nil); got != "" {
		t.Errorf("nil info: %q", got)
	}
	if got := ResolveDialect(&relaycommon.RelayInfo{}); got != "" {
		t.Errorf("info without channel meta: %q", got)
	}
	channel := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiType:              constant.APITypeOpenAI,
		ChannelOtherSettings: dto.ChannelOtherSettings{ProtocolDialect: "DeepSeek "},
	}}
	if got := ResolveDialect(channel); got != providercap.DialectDeepSeek {
		t.Errorf("channel dialect: got %q", got)
	}
	channel.ProtocolPlan = &textgen.RelayPlan{UpstreamProtocol: textgen.Protocol{Dialect: providercap.DialectOpenAI}}
	if got := ResolveDialect(channel); got != providercap.DialectOpenAI {
		t.Errorf("plan dialect should win: got %q", got)
	}
	anthropic := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiType:              constant.APITypeAnthropic,
		ChannelOtherSettings: dto.ChannelOtherSettings{ProtocolDialect: "deepseek"},
	}}
	if got := ResolveDialect(anthropic); got != providercap.DialectAnthropic {
		t.Errorf("api type must bound the dialect: got %q", got)
	}
}
