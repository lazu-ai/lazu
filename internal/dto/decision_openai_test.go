package dto

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func openAIDecisionFixture(t *testing.T, body string) *DecisionRequest {
	t.Helper()
	if body == "" {
		body = `{"model":"gpt-6-luna-decisions","input":"The screen is broken.","questions":[{"name":"损坏","type":"predicate","instructions":"Is the screen broken?"}]}`
	}
	r, err := ParseDecisionRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpenAIDecisionNativeWireAndTypedValues(t *testing.T) {
	body := `{"model":"gpt-6-luna-decisions","input":"A boolean false, not string false.","questions":[{"name":"第二项","type":"choice","instructions":"Which value?","choices":[{"value":false},{"value":"false"}]},{"type":"predicate","instructions":"Is it false?"},{"name":"","type":"predicate","instructions":"Is it boolean?"}]}`
	r := openAIDecisionFixture(t, body)
	if r.OpenAI == nil || r.Questions != nil || r.QuestionCount() != 3 || !reflect.DeepEqual(r.QuestionIDs(), []string{"第二项", "", ""}) || !reflect.DeepEqual(r.QuestionTypes(), map[string]int{"choice": 1, "predicate": 2}) {
		t.Fatalf("native shape or order changed: %+v", r)
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var before, after interface{}
	_ = json.Unmarshal([]byte(body), &before)
	_ = json.Unmarshal(encoded, &after)
	if !reflect.DeepEqual(before, after) || bytes.Contains(encoded, []byte(`"state"`)) {
		t.Fatalf("native wire changed: %s", encoded)
	}
	meta := r.GetTokenCountMeta()
	if !strings.Contains(meta.CombineText, "A boolean false") || !strings.Contains(meta.CombineText, `"value":false`) || len(meta.Files) != 0 {
		t.Fatal("native admission metadata omitted input/questions")
	}
	r.SetModelName("gpt-6-luna")
	encoded, _ = json.Marshal(r)
	if !bytes.Contains(encoded, []byte(`"model":"gpt-6-luna"`)) || r.OpenAI.Model != OpenAIDecisionModel {
		t.Fatal("model mapping mutated shared native input")
	}
}

func TestOpenAIDecisionOfficialModelUsesIsolatedInternalAlias(t *testing.T) {
	body := `{"model":"gpt-6-luna","input":"The screen is broken.","questions":[{"type":"predicate","instructions":"Is it broken?"}]}`
	r := openAIDecisionFixture(t, body)
	if r.Model != OpenAIDecisionModel || r.OpenAI.Model != OpenAIDecisionWireModel || DecisionCapabilitiesForModel(OpenAIDecisionWireModel) != nil {
		t.Fatal("official wire model must use the isolated internal decision alias")
	}
	encoded, err := json.Marshal(r)
	if err != nil || !bytes.Contains(encoded, []byte(`"model":"gpt-6-luna"`)) {
		t.Fatal("official public wire model changed", err)
	}
	if err := r.Validate(true); err != nil {
		t.Fatal("internal alias invalidated native request", err)
	}
	copyRequest := *r
	copyRequest.SetModelName("gpt-6-luna")
	encoded, _ = json.Marshal(&copyRequest)
	if r.Model != OpenAIDecisionModel || r.OpenAI.Model != OpenAIDecisionWireModel || !bytes.Contains(encoded, []byte(`"model":"gpt-6-luna"`)) {
		t.Fatal("upstream model mapping mutated the cached original")
	}
	if _, err := ParseDecisionRequest([]byte(`{"model":"gpt-6-luna","state":"x","questions":{"q":{"type":"noul","instructions":"q"}}}`)); err == nil {
		t.Fatal("ordinary chat model gained legacy decision capabilities globally")
	}
}

func TestOpenAIDecisionNativeImagesAndMetadata(t *testing.T) {
	image := decisionPNG(t)
	body := `{"model":"gpt-6-luna-decisions","input":[{"role":"user","type":"message","content":[{"type":"input_text","text":"What color?"},{"type":"input_image","image_url":"` + image + `","detail":"low"}]}],"questions":[{"type":"predicate","instructions":"Is it white?"}]}`
	r := openAIDecisionFixture(t, body)
	if len(r.ImageInfo) != 1 || r.ImageInfo[0].SHA256 == "" || r.ImageInfo[0].Detail != "low" {
		t.Fatal("validated native image metadata missing")
	}
	meta := r.GetTokenCountMeta()
	if len(meta.Files) != 1 || meta.Files[0].Detail != "low" || meta.Files[0].OriginData != image || strings.Contains(meta.CombineText, "base64") || r.InputText() != "What color?" {
		t.Fatal("image bytes duplicated into text count or detail omitted")
	}
	if err := r.Validate(true); err != nil || len(r.ImageInfo) != 1 {
		t.Fatal("repeated validation changed image count", err)
	}
	mutated := strings.Replace(body, `"detail":"low"`, `"detail":"invalid"`, 1)
	if _, err := ParseDecisionRequest([]byte(mutated)); err == nil {
		t.Fatal("accepted invalid image detail")
	}
	// Image-only user material is valid; a null content item is not.
	imageOnly := strings.Replace(body, `{"type":"input_text","text":"What color?"},`, "", 1)
	if _, err := ParseDecisionRequest([]byte(imageOnly)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`[{"role":"user","content":"valid"},{"role":"user","content":null}]`, `[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]`} {
		bad := `{"model":"gpt-6-luna-decisions","input":` + input + `,"questions":[{"type":"predicate","instructions":"q"}]}`
		if _, err := ParseDecisionRequest([]byte(bad)); err == nil {
			t.Fatal("accepted invalid user content", input)
		}
	}
}

func TestOpenAIDecisionRejectsUnsupportedOrAmbiguousInput(t *testing.T) {
	base := `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"name":"q","type":"predicate","instructions":"q"}]}`
	cases := map[string]string{
		"other_chat_model_is_not_decision_alias": strings.Replace(base, OpenAIDecisionModel, "gpt-6-sol", 1),
		"jev_cannot_use_official_wire":           strings.Replace(base, OpenAIDecisionModel, "jev-1.13", 1),
		"state_and_input":                        strings.Replace(base, `"input":"x"`, `"input":"x","state":"x"`, 1),
		"generation_not_native":                  strings.Replace(base, `"input":"x"`, `"input":"x","question_generation":{"enabled":true}`, 1),
		"stream":                                 strings.Replace(base, `"input":"x"`, `"input":"x","stream":true`, 1),
		"unknown_top_field":                      strings.Replace(base, `"input":"x"`, `"input":"x","temperature":0`, 1),
		"null_input":                             strings.Replace(base, `"input":"x"`, `"input":null`, 1),
		"object_input":                           strings.Replace(base, `"input":"x"`, `"input":{"state":"x"}`, 1),
		"empty_input":                            strings.Replace(base, `"input":"x"`, `"input":" "`, 1),
		"system_role":                            strings.Replace(base, `"input":"x"`, `"input":[{"role":"system","content":"x"}]`, 1),
		"unknown_content":                        strings.Replace(base, `"input":"x"`, `"input":[{"role":"user","content":[{"type":"input_text","text":"x","extra":1}]}]`, 1),
		"audio":                                  strings.Replace(base, `"input":"x"`, `"input":[{"role":"user","content":[{"type":"input_audio","data":"AAAA"}]}]`, 1),
		"no_questions":                           `{"model":"gpt-6-luna-decisions","input":"x"}`,
		"legacy_noul_in_native":                  strings.Replace(base, `"type":"predicate"`, `"type":"noul"`, 1),
		"unknown_question_field":                 strings.Replace(base, `"instructions":"q"`, `"instructions":"q","extra":1`, 1),
		"choice_number":                          `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":0},{"value":1}]}]}`,
		"choice_duplicate":                       `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":false},{"value":false}]}]}`,
		"choice_null":                            `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"type":"choice","instructions":"q","choices":[{"value":null},{"value":"a"}]}]}`,
		"irrelevant_options":                     strings.Replace(base, `"instructions":"q"`, `"instructions":"q","choices":[]`, 1),
		"empty_score_label":                      `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"type":"score","instructions":"q","levels":[{"label":""},{"label":"high"}]}]}`,
		"duplicate_names":                        `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"name":"q","type":"predicate","instructions":"q"},{"name":"q","type":"predicate","instructions":"q"}]}`,
		"duplicate_empty_names":                  `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"name":"","type":"predicate","instructions":"q"},{"name":"","type":"predicate","instructions":"q"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDecisionRequest([]byte(body)); err == nil {
				t.Fatal("accepted unsupported native shape")
			}
		})
	}
}

func TestOpenAIDecisionLaunchBoundsAndLegacyCompatibility(t *testing.T) {
	caps := DecisionCapabilitiesForModel(OpenAIDecisionModel)
	if caps.MaxInputTokens != 8192 || caps.MaxStateTokens != 8192 || caps.MaxImages != 4 || caps.MaxQuestions != 64 {
		t.Fatalf("launch limits changed: %+v", caps)
	}
	r := openAIDecisionFixture(t, "")
	q := r.OpenAI.Questions[0]
	q.Name = nil
	r.OpenAI.Questions = make([]OpenAIDecisionQuestion, 64)
	for i := range r.OpenAI.Questions {
		r.OpenAI.Questions[i] = q
	}
	if err := r.Validate(true); err != nil {
		t.Fatal("64 unnamed ordered questions must fit", err)
	}
	r.OpenAI.Questions = append(r.OpenAI.Questions, q)
	if err := r.Validate(true); err == nil {
		t.Fatal("accepted more than launch question limit")
	}
	legacy := decisionFixture(t, `{"model":"gpt-6-luna-decisions","state":{"number":9007199254740993},"questions":{"b":{"type":"noul","instructions":"B?"},"a":{"type":"choice","instructions":"A?","criteria":{"a":null,"b":"B"}}},"question_generation":{"enabled":true}}`)
	if legacy.OpenAI != nil || legacy.QuestionCount() != 2 || !reflect.DeepEqual(legacy.QuestionIDs(), []string{"a", "b"}) || legacy.InputText() != `{"number":9007199254740993}` {
		t.Fatal("legacy semantics changed")
	}
	encoded, _ := json.Marshal(legacy)
	if !strings.Contains(string(encoded), "9007199254740993") || !strings.Contains(string(encoded), `"state"`) || strings.Contains(string(encoded), `"input"`) {
		t.Fatal("legacy marshaling changed")
	}
}

func TestOpenAIDecisionAnswersPreserveTypedChoicesScoreAndRefusal(t *testing.T) {
	r := openAIDecisionFixture(t, `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"name":"c","type":"choice","instructions":"q","choices":[{"value":false},{"value":"false"}]},{"name":"s","type":"score","instructions":"q","levels":[{"label":"low"},{"label":"high"}]},{"type":"predicate","instructions":"q"}]}`)
	good := []json.RawMessage{
		json.RawMessage(`{"name":"c","type":"choice","choice":false,"confidence":0.8,"probabilities":[{"value":false,"probability":0.8},{"value":"false","probability":0.2}]}`),
		json.RawMessage(`{"name":"s","type":"score","score":0.75,"confidence":0.75,"probabilities":[{"value":1,"label":"high","probability":0.75},{"value":0,"label":"low","probability":0.25}]}`),
		json.RawMessage(`{"name":null,"type":"refusal","extension":"retained"}`),
	}
	if err := ValidateOpenAIDecisionAnswers(r.OpenAI.Questions, good); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		index int
		raw   string
	}{
		{"choice_missing_typed_option", 0, `{"name":"c","type":"choice","choice":false,"confidence":0.8,"probabilities":[{"value":false,"probability":0.8},{"value":false,"probability":0.2}]}`},
		{"choice_unknown_value", 0, `{"name":"c","type":"choice","choice":true,"confidence":0.8,"probabilities":[{"value":false,"probability":0.8},{"value":"false","probability":0.2}]}`},
		{"missing_confidence", 0, `{"name":"c","type":"choice","choice":false,"probabilities":[{"value":false,"probability":0.8},{"value":"false","probability":0.2}]}`},
		{"incorrect_score", 1, `{"name":"s","type":"score","score":0.2,"confidence":0.75,"probabilities":[{"value":0,"label":"low","probability":0.25},{"value":1,"label":"high","probability":0.75}]}`},
		{"incorrect_label", 1, `{"name":"s","type":"score","score":0.75,"confidence":0.75,"probabilities":[{"value":0,"label":"changed","probability":0.25},{"value":1,"label":"high","probability":0.75}]}`},
		{"score_invalid_index", 1, `{"name":"s","type":"score","score":0.75,"confidence":0.75,"probabilities":[{"value":0,"label":"low","probability":0.25},{"value":2,"label":"high","probability":0.75}]}`},
		{"predicate_no_probability", 2, `{"name":null,"type":"predicate"}`},
		{"predicate_negative", 2, `{"name":null,"type":"predicate","probability":-0.01}`},
		{"named_identity_changed", 2, `{"name":"different","type":"predicate","probability":0.8}`},
		{"refusal_not_prediction", 2, `{"name":null,"type":"refusal","probability":0.8}`},
		{"refusal_invalid_confidence", 2, `{"name":null,"type":"refusal","confidence":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answers := append([]json.RawMessage(nil), good...)
			answers[tc.index] = json.RawMessage(tc.raw)
			if err := ValidateOpenAIDecisionAnswers(r.OpenAI.Questions, answers); err == nil {
				t.Fatal("accepted corrupted provider answer")
			}
		})
	}
	if err := ValidateOpenAIDecisionAnswers(r.OpenAI.Questions, good[:2]); err == nil {
		t.Fatal("accepted missing answer")
	}
}
