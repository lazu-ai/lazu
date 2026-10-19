package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionBenchmarkBodyNativeAndLegacy(t *testing.T) {
	for _, api := range []string{"decisions-openai", "decisions-legacy"} {
		body := makeBenchmarkBody(api, "fixture-model", false, 1, strings.Repeat("a", 32))
		for _, payload := range [][]byte{body.payload(0, 0), body.payload(0, 1)} {
			var fields map[string]json.RawMessage
			if json.Unmarshal(payload, &fields) != nil || len(fields) != 3 || len(fields["stream"]) > 0 {
				t.Fatalf("invalid non-streaming body %s", payload)
			}
			field := "input"
			if api == "decisions-legacy" {
				field = "state"
			}
			var text string
			if json.Unmarshal(fields[field], &text) != nil || !strings.Contains(text, "[lazu-bench:") {
				t.Fatal("unique identity must be inside actual state/input")
			}
			if (api == "decisions-openai") != strings.HasPrefix(string(fields["questions"]), "[") {
				t.Fatal("native question ordering and legacy keyed schema differ")
			}
		}
		if string(body.payload(0, 0)) == string(body.payload(0, 1)) {
			t.Fatal("unique decision input reused")
		}
	}
}

func TestDecisionBenchmarkRejectsHTTP200ShellOrWrongFacts(t *testing.T) {
	valid := `{"model":"gpt-6-luna","answers":[{"name":"fixture","type":"predicate","probability":0.99}],"usage":{"input_tokens":20,"output_tokens":3,"total_tokens":23}}`
	for name, raw := range map[string]string{
		"valid":        valid,
		"empty":        `{}`,
		"wrong-model":  strings.Replace(valid, "gpt-6-luna", "gpt-mock", 1),
		"wrong-answer": strings.Replace(valid, "0.99", "0.01", 1),
		"bad-total":    strings.Replace(valid, `"total_tokens":23`, `"total_tokens":20`, 1),
		"trailing":     valid + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			err := validateFixtureDecision(strings.NewReader(raw), "decisions-openai", "gpt-6-luna")
			if (err == nil) != (name == "valid") {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
	legacy := `{"model":"jev-1.13","answers":{"fixture":{"type":"noul","noul":0.99}},"usage":{"input_tokens":20,"output_tokens":0}}`
	if err := validateFixtureDecision(strings.NewReader(legacy), "decisions-legacy", "jev-1.13"); err != nil {
		t.Fatal(err)
	}
}
