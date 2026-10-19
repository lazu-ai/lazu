package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestOfficialDecisionInputLimitsIncludeQuestions(t *testing.T) {
	InitTokenEncoders()
	for _, tc := range []struct {
		name      string
		input     string
		question  string
		wantError bool
	}{
		{"short", "The battery is broken.", "Is the battery broken?", false},
		{"long_input", strings.Repeat("battery ", 9000), "Is it broken?", true},
		{"long_question", "The battery is broken.", strings.Repeat("battery ", 9000), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]interface{}{
				"model": dto.OpenAIDecisionModel, "input": tc.input,
				"questions": []map[string]string{{"type": "predicate", "instructions": tc.question}},
			})
			if err != nil {
				t.Fatal(err)
			}
			r, err := dto.ParseDecisionRequest(body)
			if err != nil {
				t.Fatal(err)
			}
			if got := ValidateDecisionInputLimits(r); (got != nil) != tc.wantError {
				t.Fatalf("input bounds error=%v, want error=%v", got, tc.wantError)
			}
		})
	}
}
