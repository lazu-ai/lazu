package textgen

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The bounded degradation registry (docs/protocol-degradation.md).
//
// The protocol layer refuses what it cannot preserve. This file is the one
// exception, and it is bounded: a short list of named rules, each standing
// in for exactly one feature the channel is KNOWN not to carry (denied by the
// operator, or absent from every endpoint of the dialect), each with a reason,
// a source and a test. A rule never learns from an upstream refusal at run
// time and never changes a feature the channel might carry: unknown is not
// unsupported. Rules run in PrepareAttempt, before the upstream call, on a
// clone of the request; the client is told by X-Lazu-Warning and the request
// detail (plan warnings) that the feature was degraded, and how.

// DegradationRule is one registered stand-in.
type DegradationRule struct {
	// ID is stable and unique (snake_case); warnings, counters and the
	// registry test refer to it.
	ID string
	// Feature is the one feature the rule stands in for.
	Feature FeatureID
	// Reason is one line: what the client gets instead of the feature.
	Reason string
	// Source links the evidence that the stand-in works in practice.
	Source string
	// Applies says whether the rule can stand in on this channel for this
	// request, given what the channel cannot carry at all (unsupported).
	Applies func(snapshot CapabilitySnapshot, unsupported FeatureSet, request *Request) bool
	// Apply rewrites the request (already a clone) and returns the warning
	// path, the field the client set.
	Apply func(request *Request) string
}

// Degradations is the registry, in application order. A rule for a feature
// runs at most once per attempt.
var Degradations = []DegradationRule{
	{
		ID:      "json_schema_to_json_object",
		Feature: FeatureJSONSchema,
		Reason:  "the channel takes json_object but no schema: the answer is asked as any JSON object and the schema is stated in the instructions",
		Source:  "DeepSeek Chat Completions takes response_format text|json_object only (api-docs.deepseek.com/api/create-chat-completion, 2026-10-20); magpie's asObject does the same stand-in",
		Applies: func(snapshot CapabilitySnapshot, unsupported FeatureSet, request *Request) bool {
			// json_object asks for an object: only a schema whose root is
			// one fits it; any other root goes as an instruction alone
			return request.Output.Type == "json_schema" && objectRooted(request.Output.Schema) && !unsupported.Has(FeatureJSONOutput) && snapshot.Features.Has(FeatureJSONOutput)
		},
		Apply: func(request *Request) string {
			appendInstruction(request, schemaInstruction(request.Output))
			request.Output = OutputConstraint{Type: "json_object"}
			return "response_format"
		},
	},
	{
		ID:      "json_schema_to_instruction",
		Feature: FeatureJSONSchema,
		Reason:  "the channel takes no structured output at all: the schema is stated in the instructions and no format field is sent",
		Source:  "magpie's inSystem (github.com/yetone/magpie internal/gateway/format.go)",
		Applies: func(snapshot CapabilitySnapshot, unsupported FeatureSet, request *Request) bool {
			return request.Output.Type == "json_schema"
		},
		Apply: func(request *Request) string {
			appendInstruction(request, schemaInstruction(request.Output))
			request.Output = OutputConstraint{}
			return "response_format"
		},
	},
}

// Degrade applies the registered rules to a request on a channel: for each
// feature the request uses that the channel cannot carry at all
// (UnsupportedEverywhere), the first rule that applies rewrites a clone of
// the request and records a warning "degraded:<rule id>". The request itself
// is never changed; without a matching rule the caller gets it back as it
// came and the planner refuses as before.
func Degrade(snapshot CapabilitySnapshot, request *Request) (*Request, []Issue) {
	if request == nil {
		return nil, nil
	}
	features, _ := ExtractFeatures(request)
	unsupported := snapshot.UnsupportedEverywhere(features)
	if unsupported == 0 {
		return request, nil
	}
	var out *Request
	var issues []Issue
	done := FeatureSet(0)
	for _, rule := range Degradations {
		if !unsupported.Has(rule.Feature) || done.Has(rule.Feature) || !rule.Applies(snapshot, unsupported, request) {
			continue
		}
		if out == nil {
			out = request.Clone()
		}
		path := rule.Apply(out)
		done.Add(rule.Feature)
		issues = append(issues, Issue{Feature: rule.Feature, Path: path, Message: DegradationPrefix + rule.ID})
	}
	if out == nil {
		return request, nil
	}
	return out, issues
}

// objectRooted says whether a JSON schema asks for an object at its root:
// type object, properties without a type, or no schema at all.
func objectRooted(schema json.RawMessage) bool {
	if len(schema) == 0 {
		return true
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(schema, &root) != nil {
		return false
	}
	raw, typed := root["type"]
	if !typed {
		_, hasProperties := root["properties"]
		return hasProperties
	}
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return name == "object"
	}
	var names []string
	if json.Unmarshal(raw, &names) == nil {
		object := false
		for _, name := range names {
			switch name {
			case "object":
				object = true
			case "null":
			default:
				return false
			}
		}
		return object
	}
	return false
}

// schemaInstruction is the structured-output ask said in words.
func schemaInstruction(output OutputConstraint) string {
	if len(output.Schema) > 0 {
		return "Respond with a single JSON value and nothing else, matching this JSON schema:\n" + strings.TrimSpace(string(output.Schema)) + "."
	}
	return "Respond with a single JSON object and nothing else."
}

// appendInstruction adds text to the request's instructions: on the end of
// the last one when there is one (a Chat->Responses bridge carries exactly
// one instructions field), as a new system instruction otherwise.
func appendInstruction(request *Request, text string) {
	if n := len(request.Instructions); n > 0 {
		last := &request.Instructions[n-1]
		last.Text = strings.TrimRight(last.Text, "\n") + "\n\n" + text
		return
	}
	request.Instructions = []Instruction{{Role: "system", Text: text}}
}

// DegradationIDs lists the registered rule IDs, for the registry test and
// the docs.
func DegradationIDs() []string {
	out := make([]string, 0, len(Degradations))
	for _, rule := range Degradations {
		out = append(out, rule.ID)
	}
	return out
}

func (r DegradationRule) String() string { return fmt.Sprintf("%s (%s)", r.ID, r.Feature.String()) }
