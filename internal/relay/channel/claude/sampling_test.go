package claude

import (
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
)

// refusing is a catalog row for an adaptive Claude whose unsupported_params
// list temperature and top_p; listing is the same row listing nothing.
var (
	refusing = StaticPolicy(ModelPolicy{
		Reasoning:       model.ModelReasoningOptions{Mode: model.ReasoningModeAdaptive, Off: model.ReasoningOffUpToHigh},
		RefusedSampling: []string{"temperature", "top_p"},
		Known:           true,
	})
	listing = StaticPolicy(ModelPolicy{
		Reasoning: model.ModelReasoningOptions{Mode: model.ReasoningModeAdaptive, Off: model.ReasoningOffUpToHigh},
		Known:     true,
	})
)

func TestRefusedSampling(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params []string
		want   []string
	}{
		{"listed fields, spelled loosely", []string{" Top_P ", "temperature", "stream"}, []string{"temperature", "top_p"}},
		{"only sampling fields", []string{"stream", "tools", "safety_identifier"}, nil},
		{"nothing listed", nil, nil},
		{"every sampling field", []string{"top_k", "top_p", "temperature"}, []string{"temperature", "top_p", "top_k"}},
	} {
		if got := refusedSampling(tc.params); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: refusedSampling(%q) = %q, want %q", tc.name, tc.params, got, tc.want)
		}
	}
}

// The catalog resolver carries the row's refused sampling fields into the
// policy, found on the upstream name or, when the channel mapped it, on the
// client's; a row listing nothing or no row at all refuses nothing.
func TestCatalogPolicyCarriesRefusedSampling(t *testing.T) {
	rows := map[string][]string{
		"claude-haiku-5-5":          {"temperature"},
		"claude-haiku-4-5-20251001": nil,
	}
	describe := func(name string) textgenruntime.ModelDescriptor {
		params, ok := rows[name]
		return textgenruntime.ModelDescriptor{UnsupportedParams: params, Found: ok}
	}
	noReasoning := func(...string) model.ModelReasoningOptions { return model.ModelReasoningOptions{} }
	for _, tc := range []struct {
		name, origin, asked string
		want                []string
	}{
		{"row lists temperature", "claude-haiku-5-5", "claude-haiku-5-5", []string{"temperature"}},
		{"mapped upstream unknown, origin decides", "claude-haiku-5-5", "lane-haiku", []string{"temperature"}},
		{"row lists nothing", "claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001", nil},
		{"not in the catalog", "lane-haiku", "lane-haiku", nil},
	} {
		got := policyResolver(tc.origin, noReasoning, describe)(tc.asked).RefusedSampling
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: RefusedSampling = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDropRefusedSampling(t *testing.T) {
	request := &dto.ClaudeRequest{Model: "claude-haiku-5-5", Temperature: common.GetPointer(0.2), TopP: 0.9, TopK: 5}
	if got := DropRefusedSampling(request, refusing); !reflect.DeepEqual(got, []string{"temperature", "top_p"}) {
		t.Fatalf("dropped = %q", got)
	}
	if request.Temperature != nil || request.TopP != 0 {
		t.Fatalf("listed fields must go: temperature=%v top_p=%v", request.Temperature, request.TopP)
	}
	if request.TopK != 5 {
		t.Fatalf("top_k is not listed and must stay, got %d", request.TopK)
	}

	onlyTemperature := &dto.ClaudeRequest{Model: "claude-haiku-5-5", Temperature: common.GetPointer(0.2)}
	if got := DropRefusedSampling(onlyTemperature, refusing); !reflect.DeepEqual(got, []string{"temperature"}) {
		t.Fatalf("only the fields the request carried are named: %q", got)
	}

	kept := &dto.ClaudeRequest{Model: "claude-haiku-4-5-20251001", Temperature: common.GetPointer(0.2), TopP: 0.9}
	if got := DropRefusedSampling(kept, listing); got != nil || kept.Temperature == nil || *kept.Temperature != 0.2 || kept.TopP != 0.9 {
		t.Fatalf("a row that lists nothing drops nothing: %q %+v", got, kept)
	}

	asSent := &dto.ClaudeRequest{Model: "lane-haiku", Temperature: common.GetPointer(0.2)}
	if got := DropRefusedSampling(asSent, nil); got != nil || asSent.Temperature == nil {
		t.Fatalf("no resolver: sent as it came, got %q %+v", got, asSent)
	}
	if got := DropRefusedSampling(nil, refusing); got != nil {
		t.Fatalf("nil request: %q", got)
	}
}

// A Chat client's temperature to a model whose row refuses it is left out
// of the bridged Messages request; a model whose row lists nothing keeps it.
// The drop runs after the thinking is fitted, so the temperature 1 that a
// budget writes goes too.
func TestBridgedRequestDropsSamplingTheCatalogRefuses(t *testing.T) {
	temperature := 0.2
	topP := 0.9
	dropped, err := bridgeChat(dto.GeneralOpenAIRequest{Model: "claude-haiku-5-5", MaxTokens: 100, Temperature: &temperature, TopP: &topP}, refusing)
	if err != nil {
		t.Fatal(err)
	}
	if dropped.Temperature != nil || dropped.TopP != 0 {
		t.Fatalf("temperature and top_p must not reach Messages for a refusing model: %+v", dropped)
	}

	kept, err := bridgeChat(dto.GeneralOpenAIRequest{Model: "claude-haiku-4-5-20251001", MaxTokens: 100, Temperature: &temperature, TopP: &topP}, listing)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Temperature == nil || *kept.Temperature != 0.2 || kept.TopP != 0.9 {
		t.Fatalf("a row that lists nothing keeps the client's sampling: %+v", kept)
	}

	refusingBudget := StaticPolicy(ModelPolicy{
		Reasoning:       model.ModelReasoningOptions{Mode: model.ReasoningModeBudget, Off: model.ReasoningOffDisabled},
		RefusedSampling: []string{"temperature"},
		Known:           true,
	})
	thinking, err := bridgeChat(dto.GeneralOpenAIRequest{Model: "claude-haiku-5-5", MaxTokens: 4000, ReasoningEffort: "low", Temperature: &temperature}, refusingBudget)
	if err != nil {
		t.Fatal(err)
	}
	if thinking.Thinking == nil || thinking.Thinking.Type != "enabled" {
		t.Fatalf("expected budget thinking, got %+v", thinking.Thinking)
	}
	if thinking.Temperature != nil {
		t.Fatalf("the temperature 1 a budget writes is dropped too: %v", *thinking.Temperature)
	}
}
