package textgen

import (
	"encoding/json"
	"strings"
	"testing"
)

func schemaRequest() *Request {
	request := minimalRequest()
	request.Instructions = []Instruction{{Role: "system", Text: "You name sessions."}}
	request.Output = OutputConstraint{Type: "json_schema", Name: "session_title", Schema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`), Strict: Some(true)}
	return request
}

func deniedJSONSchema(dialect string, withJSONObject bool, endpoints ...FeatureID) CapabilitySnapshot {
	snapshot := snapshot(dialect, endpoints...)
	if withJSONObject {
		snapshot.Features.Add(FeatureJSONOutput)
	}
	snapshot.Denied.Add(FeatureJSONSchema)
	snapshot.Denied.Add(FeatureStrictSchema)
	return snapshot
}

// Every registered rule has an ID, a reason, a source, and a case in this
// table (AGENTS.md, Protocol Layer rule 5).
func TestDegradationRegistryIsComplete(t *testing.T) {
	cases := map[string]bool{"json_schema_to_json_object": true, "json_schema_to_instruction": true, "json_object_to_instruction": true}
	seen := map[string]bool{}
	for _, rule := range Degradations {
		if rule.ID == "" || rule.Reason == "" || rule.Source == "" || rule.Applies == nil || rule.Apply == nil {
			t.Fatalf("rule %q is missing a field: %+v", rule.ID, rule)
		}
		if seen[rule.ID] {
			t.Fatalf("rule %q registered twice", rule.ID)
		}
		seen[rule.ID] = true
		if !cases[rule.ID] {
			t.Fatalf("rule %q has no case in TestDegradationRegistryIsComplete; add one with its test", rule.ID)
		}
	}
	for id := range cases {
		if !seen[id] {
			t.Fatalf("test table names %q but the registry does not", id)
		}
	}
}

func TestDegradeJSONSchemaToJSONObjectWhenTheChannelTakesObjects(t *testing.T) {
	request := schemaRequest()
	out, issues := Degrade(deniedJSONSchema("openai_chat_compatible", true, FeatureEndpointChat), request)
	if out == request {
		t.Fatal("request must be cloned, not rewritten")
	}
	if request.Output.Type != "json_schema" || len(request.Instructions) != 1 || strings.Contains(request.Instructions[0].Text, "JSON schema") {
		t.Fatalf("original changed: %+v %+v", request.Output, request.Instructions)
	}
	if out.Output.Type != "json_object" || out.Output.Schema != nil || out.Output.Strict.Set {
		t.Fatalf("expected json_object with no schema, got %+v", out.Output)
	}
	if len(out.Instructions) != 1 || !strings.HasPrefix(out.Instructions[0].Text, "You name sessions.\n\n") || !strings.Contains(out.Instructions[0].Text, `"title"`) {
		t.Fatalf("schema must be spoken at the end of the one instruction, got %+v", out.Instructions)
	}
	if len(issues) != 1 || issues[0].Message != "degraded:json_schema_to_json_object" || issues[0].Path != "response_format" || issues[0].Feature != FeatureJSONSchema {
		t.Fatalf("unexpected warnings: %+v", issues)
	}
}

func TestDegradeJSONSchemaToInstructionWhenNothingStructuredIsTaken(t *testing.T) {
	request := schemaRequest()
	request.Instructions = nil
	out, issues := Degrade(deniedJSONSchema("openai_chat_compatible", false, FeatureEndpointChat), request)
	if out.Output.Type != "" || len(out.Instructions) != 1 || out.Instructions[0].Role != "system" {
		t.Fatalf("expected no format and one system instruction, got %+v %+v", out.Output, out.Instructions)
	}
	if len(issues) != 1 || issues[0].Message != "degraded:json_schema_to_instruction" {
		t.Fatalf("unexpected warnings: %+v", issues)
	}
}

// A feature the channel might carry (unknown) is never degraded; only what is
// denied or gapped on every endpoint.
func TestDegradeLeavesUnknownAndRoutableFeaturesAlone(t *testing.T) {
	request := schemaRequest()
	if out, issues := Degrade(snapshot("openai_chat_compatible", FeatureEndpointChat), request); out != request || issues != nil {
		t.Fatalf("unknown support must not degrade: %+v", issues)
	}
	gapped := snapshot("deepseek", FeatureEndpointChat, FeatureEndpointResponses)
	gapped.Features.Add(FeatureJSONSchema)
	gapped.Features.Add(FeatureJSONOutput)
	gapped.EndpointGaps = map[FeatureID]FeatureSet{FeatureEndpointChat: FeatureSet(1) << FeatureJSONSchema}
	if out, issues := Degrade(gapped, request); out != request || issues != nil {
		t.Fatalf("a feature the other endpoint carries is routed, not degraded: %+v", issues)
	}
	gapped.Features &^= FeatureSet(1) << FeatureEndpointResponses
	if out, issues := Degrade(gapped, request); out == request || len(issues) != 1 || issues[0].Message != "degraded:json_schema_to_json_object" {
		t.Fatalf("gapped on the only endpoint must degrade: %+v", issues)
	}
	plain := minimalRequest()
	if out, issues := Degrade(deniedJSONSchema("openai_chat_compatible", true, FeatureEndpointChat), plain); out != plain || issues != nil {
		t.Fatal("a request without the feature is untouched")
	}
}

// json_object asks for an object, so a schema with another root goes as an
// instruction alone.
func TestDegradeKeepsJSONObjectForObjectRootsOnly(t *testing.T) {
	snapshot := CapabilitySnapshot{Dialect: "openai_chat_compatible", Features: FeatureSet(1)<<FeatureEndpointChat | FeatureSet(1)<<FeatureJSONOutput, Denied: FeatureSet(1) << FeatureJSONSchema}
	for schema, want := range map[string]string{
		`{"type":"object","properties":{}}`: "degraded:json_schema_to_json_object",
		`{"type":"array","items":{}}`:       "degraded:json_schema_to_instruction",
		`{"type":"string"}`:                 "degraded:json_schema_to_instruction",
	} {
		request := &Request{Output: OutputConstraint{Type: "json_schema", Schema: []byte(schema)}}
		_, issues := Degrade(snapshot, request)
		if len(issues) != 1 || issues[0].Message != want {
			t.Fatalf("%s: %+v", schema, issues)
		}
	}
}

// A type list without object is not an object root.
func TestObjectRootedNeedsAnObjectMember(t *testing.T) {
	if objectRooted([]byte(`{"type":["null"]}`)) || !objectRooted([]byte(`{"type":["object","null"]}`)) {
		t.Fatal("a type list is an object root only with object in it")
	}
}

// messagesOnlySnapshot is an Anthropic channel as providercap builds it: the
// Messages endpoint alone, which takes json_schema but no bare json_object.
func messagesOnlySnapshot() CapabilitySnapshot {
	snapshot := snapshot("anthropic", FeatureEndpointMessages)
	snapshot.Features.Add(FeatureJSONSchema)
	snapshot.Features.Add(FeatureJSONOutput)
	snapshot.EndpointGaps = map[FeatureID]FeatureSet{FeatureEndpointMessages: FeatureSet(1) << FeatureJSONOutput}
	return snapshot
}

// json_object on a channel whose only endpoint lacks it is asked for in the
// instructions; the format field goes. A Chat client's release-note request
// to a Claude model is this case.
func TestDegradeJSONObjectToInstructionWhenNoEndpointCarriesIt(t *testing.T) {
	request := minimalRequest()
	request.Instructions = []Instruction{{Role: "system", Text: "You write release notes."}}
	request.Output = OutputConstraint{Type: "json_object"}
	out, issues := Degrade(messagesOnlySnapshot(), request)
	if out == request || request.Output.Type != "json_object" || len(request.Instructions) != 1 || strings.Contains(request.Instructions[0].Text, "JSON") {
		t.Fatalf("request must be cloned, not rewritten: %+v %+v", request.Output, request.Instructions)
	}
	if out.Output.Type != "" || out.Output.Schema != nil || out.Output.Strict.Set {
		t.Fatalf("expected no format field, got %+v", out.Output)
	}
	if len(out.Instructions) != 1 || out.Instructions[0].Text != "You write release notes.\n\nRespond with a single JSON object and nothing else." {
		t.Fatalf("the ask must be spoken at the end of the one instruction, got %+v", out.Instructions)
	}
	if len(issues) != 1 || issues[0].Message != "degraded:json_object_to_instruction" || issues[0].Path != "response_format" || issues[0].Feature != FeatureJSONOutput {
		t.Fatalf("unexpected warnings: %+v", issues)
	}
	// without an instruction, the ask is a new system instruction
	bare := minimalRequest()
	bare.Output = OutputConstraint{Type: "json_object"}
	if out, _ := Degrade(messagesOnlySnapshot(), bare); len(out.Instructions) != 1 || out.Instructions[0].Role != "system" || out.Instructions[0].Text != "Respond with a single JSON object and nothing else." {
		t.Fatalf("expected one system instruction, got %+v", out.Instructions)
	}
	// unknown is not unsupported: a Chat channel with no gap and no denial
	// sends json_object as it came
	plain := minimalRequest()
	plain.Output = OutputConstraint{Type: "json_object"}
	if out, issues := Degrade(snapshot("openai_chat_compatible", FeatureEndpointChat), plain); out != plain || issues != nil {
		t.Fatalf("json_object on a channel that may carry it is untouched: %+v", issues)
	}
}

// A schema denied on a channel with no bare json_object anywhere goes as an
// instruction alone: json_schema_to_json_object would hand the Messages
// bridge the one format it refuses.
func TestDegradeDeniedSchemaSkipsJSONObjectWhereNoEndpointCarriesIt(t *testing.T) {
	snapshot := messagesOnlySnapshot()
	snapshot.Denied.Add(FeatureJSONSchema)
	out, issues := Degrade(snapshot, schemaRequest())
	if out.Output.Type != "" || len(issues) != 1 || issues[0].Message != "degraded:json_schema_to_instruction" {
		t.Fatalf("expected the schema as an instruction alone, got %+v %+v", out.Output, issues)
	}
}
