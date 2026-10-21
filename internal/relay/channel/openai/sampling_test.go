package openai

import (
	"bytes"
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	textgenruntime "github.com/lazu-ai/lazu/internal/protocol/textgen/runtime"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/tidwall/gjson"
)

// stubCatalog serves rows as the catalog would: gpt-6-luna and gpt-6-astra
// list temperature (as production does), gpt-6-sol also top_p, gpt-4.1 lists
// nothing, and any other name is not in the catalog.
func stubCatalog(t *testing.T) {
	t.Helper()
	rows := map[string][]string{
		"gpt-6-luna":  {"temperature"},
		"gpt-6-astra": {"temperature"},
		"gpt-6-sol":   {"temperature", "top_p"},
		"gpt-4.1":     nil,
	}
	previous := describeModel
	describeModel = func(name string) textgenruntime.ModelDescriptor {
		params, ok := rows[name]
		return textgenruntime.ModelDescriptor{UnsupportedParams: params, Found: ok}
	}
	t.Cleanup(func() { describeModel = previous })
}

func samplingInfo(upstream, origin string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: origin,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: upstream},
		ProtocolPlan:    &textgen.RelayPlan{Native: true, UpstreamProtocol: textgen.Protocol{Dialect: "openai"}},
	}
}

func TestNativeRequestDropsSamplingTheCatalogRefusesWhileReasoning(t *testing.T) {
	stubCatalog(t)
	for _, tc := range []struct {
		name, model, origin, body string
		responses                 bool
		dropped, kept             []string
	}{
		{"chat, effort unnamed", "gpt-6-luna", "", `{"model":"gpt-6-luna","temperature":0,"messages":[{"role":"user","content":"hi"}]}`, false, []string{"temperature"}, nil},
		{"chat, effort high", "gpt-6-luna", "", `{"model":"gpt-6-luna","temperature":0,"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`, false, []string{"temperature"}, nil},
		{"chat, reasoning object", "gpt-6-luna", "", `{"model":"gpt-6-luna","temperature":0,"reasoning":{"effort":"low"},"messages":[{"role":"user","content":"hi"}]}`, false, []string{"temperature"}, nil},
		{"chat, effort none keeps it", "gpt-6-luna", "", `{"model":"gpt-6-luna","temperature":0,"reasoning_effort":"none","messages":[{"role":"user","content":"hi"}]}`, false, nil, []string{"temperature"}},
		{"chat, only listed fields", "gpt-6-luna", "", `{"model":"gpt-6-luna","temperature":0,"top_p":0.5,"messages":[{"role":"user","content":"hi"}]}`, false, []string{"temperature"}, []string{"top_p"}},
		{"chat, top_p listed", "gpt-6-sol", "", `{"model":"gpt-6-sol","temperature":0,"top_p":0.5,"messages":[{"role":"user","content":"hi"}]}`, false, []string{"temperature", "top_p"}, nil},
		{"chat, mapped name found by origin", "lane-gpt6", "gpt-6-luna", `{"model":"gpt-6-luna","temperature":0,"messages":[{"role":"user","content":"hi"}]}`, false, []string{"temperature"}, nil},
		{"chat, row lists nothing", "gpt-4.1", "", `{"model":"gpt-4.1","temperature":0,"messages":[{"role":"user","content":"hi"}]}`, false, nil, []string{"temperature"}},
		{"chat, not in catalog", "vendor-model", "", `{"model":"vendor-model","temperature":0,"messages":[{"role":"user","content":"hi"}]}`, false, nil, []string{"temperature"}},
		{"responses, effort unnamed", "gpt-6-luna", "", `{"model":"gpt-6-luna","temperature":0,"input":"hi"}`, true, []string{"temperature"}, nil},
		{"responses, effort none keeps it", "gpt-6-luna", "", `{"model":"gpt-6-luna","temperature":0,"reasoning":{"effort":"none"},"input":"hi"}`, true, nil, []string{"temperature"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte(tc.body)
			original := bytes.Clone(input)
			got, err := (&Adaptor{}).ConvertNativeRequest(input, samplingInfo(tc.model, tc.origin), tc.responses)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range tc.dropped {
				if gjson.GetBytes(got, field).Exists() {
					t.Fatalf("%s sent while reasoning: %s", field, got)
				}
			}
			for _, field := range tc.kept {
				if gjson.GetBytes(got, field).Raw != gjson.GetBytes(original, field).Raw {
					t.Fatalf("%s changed: %s", field, got)
				}
			}
			if gjson.GetBytes(got, "messages").Raw != gjson.GetBytes(original, "messages").Raw || gjson.GetBytes(got, "input").Raw != gjson.GetBytes(original, "input").Raw {
				t.Fatalf("caller content changed: %s", got)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("mutated original native body")
			}
		})
	}
}

func TestNativeResponsesWithoutRefusedSamplingKeepsByteIdentity(t *testing.T) {
	stubCatalog(t)
	body := []byte("{ \"model\" : \"gpt-6-luna\", \"temperature\":0, \"reasoning\":{\"effort\":\"none\"}, \"input\":\"hello\" }\n")
	got, err := (&Adaptor{}).ConvertNativeRequest(body, samplingInfo("gpt-6-luna", ""), true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, got) {
		t.Fatalf("native byte identity lost: %s", got)
	}
}

func TestConvertOpenAIRequestDropsSamplingAfterTheEffortIsSettled(t *testing.T) {
	stubCatalog(t)
	zero := 0.0
	tools := []dto.ToolCallRequest{{Type: "function", Function: dto.FunctionRequest{Name: "weather"}}}
	for _, tc := range []struct {
		name        string
		model       string
		effort      string
		tools       []dto.ToolCallRequest
		wantDropped bool
	}{
		{"effort unnamed", "gpt-6-luna", "", nil, true},
		{"effort none", "gpt-6-luna", "none", nil, false},
		// tools turn reasoning off for gpt-6 on Chat, so temperature goes too
		{"tools turn reasoning off", "gpt-6-luna", "", tools, false},
		// astra cannot turn reasoning off, so tools leave it reasoning
		{"astra with tools", "gpt-6-astra", "", tools, true},
		{"not in catalog", "vendor-model", "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{Model: tc.model, Temperature: &zero, ReasoningEffort: tc.effort, Tools: tc.tools}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, samplingInfo(tc.model, ""), request)
			if err != nil {
				t.Fatal(err)
			}
			got := converted.(*dto.GeneralOpenAIRequest)
			if dropped := got.Temperature == nil; dropped != tc.wantDropped {
				t.Fatalf("temperature dropped = %v, want %v (effort %q)", dropped, tc.wantDropped, got.ReasoningEffort)
			}
		})
	}
}

func TestConvertOpenAIResponsesRequestDropsSamplingWhileReasoning(t *testing.T) {
	stubCatalog(t)
	zero, half := 0.0, 0.5
	for _, tc := range []struct {
		name      string
		model     string
		reasoning *dto.Reasoning
		wantTemp  bool
		wantTopP  bool
	}{
		{"effort unnamed", "gpt-6-sol", nil, false, false},
		{"effort high", "gpt-6-sol", &dto.Reasoning{Effort: "high"}, false, false},
		{"effort none", "gpt-6-sol", &dto.Reasoning{Effort: "none"}, true, true},
		{"row lists only temperature", "gpt-6-luna", nil, false, true},
		{"not in catalog", "vendor-model", nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := dto.OpenAIResponsesRequest{Model: tc.model, Temperature: &zero, TopP: &half, Reasoning: tc.reasoning}
			converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, samplingInfo(tc.model, ""), request)
			if err != nil {
				t.Fatal(err)
			}
			got := converted.(dto.OpenAIResponsesRequest)
			if (got.Temperature != nil) != tc.wantTemp || (got.TopP != nil) != tc.wantTopP {
				t.Fatalf("temperature kept = %v, top_p kept = %v; want %v, %v", got.Temperature != nil, got.TopP != nil, tc.wantTemp, tc.wantTopP)
			}
		})
	}
}
