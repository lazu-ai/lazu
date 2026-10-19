package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	usagecalc "github.com/lazu-ai/lazu/internal/usage"
	"github.com/lazu-ai/lazu/pkg/billing"
)

func officialDecisionFixture(t *testing.T, body string) *dto.DecisionRequest {
	t.Helper()
	if body == "" {
		body = `{"model":"gpt-6-luna-decisions","input":"The switch is off.","questions":[{"name":"off","type":"predicate","instructions":"Is the switch off?"}]}`
	}
	r, err := dto.ParseDecisionRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpenAIDecisionBuildNativeRequest(t *testing.T) {
	r := officialDecisionFixture(t, `{"model":"gpt-6-luna-decisions","input":[{"role":"user","content":"False is boolean."}],"questions":[{"name":"布尔","type":"choice","instructions":"Which type?","choices":[{"value":false},{"value":"false","description":"the string"}]},{"type":"predicate","instructions":"Is it false?"}]}`)
	for _, tc := range []struct{ base, host, path, model string }{
		{"", "api.openai.com", "/v1/decisions", "gpt-6-luna"},
		{"https://provider.test/v1/", "provider.test", "/v1/decisions", dto.OpenAIDecisionModel},
		{"https://provider.test/proxy/v1", "provider.test", "/proxy/v1/decisions", "gpt-6-luna"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := GetAdaptor("openai").BuildRequest(ctx, tc.base, "", "fixture-only", tc.model, r)
			if err != nil {
				t.Fatal(err)
			}
			if req.URL.Host != tc.host || req.URL.Path != tc.path || req.Method != "POST" || req.Context() != ctx || req.Header.Get("Authorization") != "Bearer fixture-only" {
				t.Fatalf("wrong official request: %v", req.URL)
			}
			raw, _ := io.ReadAll(req.Body)
			var wire dto.OpenAIDecisionRequest
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &wire) != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 3 || wire.Model != "gpt-6-luna" || !reflect.DeepEqual(wire.Questions, r.OpenAI.Questions) {
				t.Fatalf("official typed questions or provider-only schema changed: %s", raw)
			}
			if !jsonEqual(wire.Input, r.OpenAI.Input) || r.OpenAI.Model != dto.OpenAIDecisionModel {
				t.Fatal("native input/order or shared public model mutated")
			}
			cancel()
			if req.Context().Err() != context.Canceled {
				t.Fatal("provider request lost cancellation")
			}
		})
	}
	for _, dialect := range []string{"typesafe", "openrouter", "cloudflare_workers_ai"} {
		if _, err := GetAdaptor(dialect).BuildRequest(context.Background(), "https://provider.test", "acct", "fixture-only", "gpt-6-luna", r); err == nil {
			t.Fatal("official input emitted using incompatible dialect", dialect)
		}
	}
	if _, err := GetAdaptor("openai").BuildRequest(context.Background(), "", "", "fixture-only", "gpt-6-luna-chat", r); err == nil {
		t.Fatal("unsupported upstream model accepted")
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y interface{}
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestOpenAIDecisionLegacyBridgePreservesBusinessJSON(t *testing.T) {
	r := officialDecisionFixture(t, `{"model":"gpt-6-luna-decisions","state":{"number":9007199254740993},"questions":{"z":{"type":"score","instructions":{"rubric":"severity"},"criteria":[{"number":9007199254740993},"high"]},"a":{"type":"noul","instructions":"Is it true?","criteria":{"true":{"number":9007199254740993},"false":null}},"m":{"type":"choice","instructions":"Choose.","criteria":{"z":null,"a":{"number":9007199254740993}}}},"question_generation":{"enabled":false}}`)
	req, err := GetAdaptor("openai").BuildRequest(context.Background(), "", "", "fixture-only", "gpt-6-luna", r)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(req.Body)
	var wire dto.OpenAIDecisionRequest
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	var input string
	_ = json.Unmarshal(wire.Input, &input)
	if input != `{"number":9007199254740993}` || len(wire.Questions) != 3 || strings.Contains(string(raw), "question_generation") {
		t.Fatalf("business JSON precision or controls leaked: %s", raw)
	}
	if err := wire.Validate(); err != nil {
		t.Fatal("legacy bridge produced invalid official request", err)
	}
	if *wire.Questions[0].Name != "a" || wire.Questions[0].Type != "predicate" || !strings.Contains(wire.Questions[0].Instructions, `"number":9007199254740993`) {
		t.Fatal("noul truth criteria/name lost")
	}
	c := wire.Questions[1].Choices
	if *wire.Questions[1].Name != "m" || string(c[0].Value) != `"a"` || *c[0].Description != `{"number":9007199254740993}` || string(c[1].Value) != `"z"` || c[1].Description != nil {
		t.Fatal("choice values/nullable descriptions changed")
	}
	s := wire.Questions[2]
	if *s.Name != "z" || s.Levels[0].Label != "0" || *s.Levels[0].Description != `{"number":9007199254740993}` || *s.Levels[1].Description != "high" {
		t.Fatal("ordered rubric precision lost")
	}
	// Inline images use the official message schema, not a legacy images field.
	r.ImageInfo = []dto.DecisionImage{{DataURL: "data:image/png;base64,fixture"}}
	official, err := openAIRequest(r, "gpt-6-luna")
	if err != nil || !bytes.Contains(official.Input, []byte(`"input_image"`)) || !bytes.Contains(official.Input, []byte(`"input_text"`)) {
		t.Fatal("legacy inline images not bridged", err)
	}
}

func TestOpenAIDecisionNativeResponsePreservesUsageAndRefusal(t *testing.T) {
	r := officialDecisionFixture(t, `{"model":"gpt-6-luna-decisions","input":"x","questions":[{"name":"c","type":"choice","instructions":"q","choices":[{"value":false},{"value":"false"}]},{"type":"predicate","instructions":"Unobservable?"}]}`)
	body := `{"id":"dec_fixture","model":"gpt-6-luna","object":"decision","answers":[{"name":"c","type":"choice","choice":false,"confidence":0.8,"probabilities":[{"value":false,"probability":0.8},{"value":"false","probability":0.2}],"extension":9007199254740993},{"name":null,"type":"refusal","reason":"insufficient evidence"}],"usage":{"input_tokens":171,"output_tokens":2,"total_tokens":173,"input_tokens_details":{"cached_tokens":40,"cache_write_tokens":20},"output_tokens_details":{"reasoning_tokens":1},"provider_extension":9007199254740993},"extension":9007199254740993}`
	result, err := GetAdaptor("openai").ParseDecisionResponse([]byte(body), r)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gpt-6-luna" || result.Usage.InputTokens != 171 || result.Usage.OutputTokens != 2 || len(result.Answers) != 0 {
		t.Fatal("upstream model/usage or native answer shape changed")
	}
	u := result.ProviderUsage
	if u == nil || u.PromptTokens != 171 || u.CompletionTokens != 2 || u.TotalTokens != 173 || u.PromptTokensDetails.CachedTokens != 40 || u.PromptTokensDetails.CacheWriteTokens != 20 || u.CompletionTokenDetails.ReasoningTokens != 1 || u.InputTokenSource != dto.UsageSourceProvider || u.OutputTokenSource != dto.UsageSourceProvider {
		t.Fatalf("provider cache/usage normalization incomplete: %+v", u)
	}
	var original, emitted map[string]json.RawMessage
	_ = json.Unmarshal([]byte(body), &original)
	_ = json.Unmarshal(result.NativeResponse, &emitted)
	original["model"] = json.RawMessage(`"gpt-6-luna-decisions"`)
	for key, value := range original {
		if !bytes.Equal(bytes.TrimSpace(value), bytes.TrimSpace(emitted[key])) {
			t.Fatalf("native field %s changed or lost: %s", key, emitted[key])
		}
	}
	if len(original) != len(emitted) {
		t.Fatal("unexpected native response fields")
	}
}

func TestOpenAIDecisionOfficialResponseRetainsPublicModel(t *testing.T) {
	r := officialDecisionFixture(t, `{"model":"gpt-6-luna","input":"x","questions":[{"type":"predicate","instructions":"q"}]}`)
	result, err := GetAdaptor("openai").ParseDecisionResponse([]byte(`{"model":"gpt-6-luna","answers":[{"name":null,"type":"predicate","probability":1}],"usage":{"input_tokens":16,"output_tokens":0,"total_tokens":16}}`), r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(result.NativeResponse, &fields)
	if string(fields["model"]) != `"gpt-6-luna"` || result.Model != "gpt-6-luna" || r.Model != dto.OpenAIDecisionModel {
		t.Fatal("public model must remain official while internal routing uses alias")
	}
}

func TestOpenAIDecisionProviderUsageBillingDoesNotDoubleCount(t *testing.T) {
	r := officialDecisionFixture(t, "")
	result, err := GetAdaptor("openai").ParseDecisionResponse([]byte(`{"model":"gpt-6-luna","answers":[{"name":"off","type":"refusal"}],"usage":{"input_tokens":1000,"output_tokens":0,"total_tokens":1000,"input_tokens_details":{"cached_tokens":200,"image_tokens":100,"text_tokens":700},"output_tokens_details":{"reasoning_tokens":0}}}`), r)
	if err != nil {
		t.Fatal(err)
	}
	summary := usagecalc.SummaryFromDTO(result.ProviderUsage)
	u := usagecalc.BillingUsageFromSummary(summary)
	if summary.PromptTokens != 1000 || summary.ImageInputTokens != 100 || summary.CacheReadTokens != 200 || summary.InputTokens != 700 || u.InputTokenTotal() != 1000 || u.OutputTokens != 0 {
		t.Fatalf("provider usage was lost or double counted: %+v", summary)
	}
	input, image, cache, output := billing.MicroUSD(120000), billing.MicroUSD(120000), billing.MicroUSD(0), billing.MicroUSD(0)
	quote := billing.Quote(u, billing.PriceSheet{ModelName: dto.OpenAIDecisionModel, InputPerMtokMicrousd: &input, ImageInputPerMtokMicrousd: &image, CacheReadPerMtokMicrousd: &cache, OutputPerMtokMicrousd: &output}, nil)
	if quote.TotalMicrousd != 96 || quote.SubtotalMicrousd != 96 || len(quote.MissingPriceDimensions) != 0 {
		t.Fatalf("700 fresh + 100 image at $0.12/Mtok and 200 cached at zero must cost 96 microUSD: %+v", quote)
	}
	amounts := map[string]billing.MicroUSD{}
	for _, item := range quote.LineItems {
		amounts[item.Dimension] = item.AmountMicrousd
	}
	if amounts["input"] != 84 || amounts["image_input"] != 12 || amounts["cache_read"] != 0 {
		t.Fatalf("billing line items duplicated modalities/cache: %+v", amounts)
	}
}

func TestOpenAIDecisionLegacyResponseAndRefusal(t *testing.T) {
	r := officialDecisionFixture(t, `{"model":"gpt-6-luna-decisions","state":"x","questions":{"p":{"type":"noul","instructions":"q"},"c":{"type":"choice","instructions":"q","criteria":{"a":null,"b":"B"}},"s":{"type":"score","instructions":"q","criteria":[{"number":9007199254740993},"high"]}}}`)
	body := `{"model":"gpt-6-luna","answers":[{"name":"c","type":"choice","choice":"b","confidence":0.8,"probabilities":[{"value":"a","probability":0.2},{"value":"b","probability":0.8}]},{"name":"p","type":"predicate","probability":0.7,"extension":9007199254740993},{"name":"s","type":"score","score":0.75,"confidence":0.75,"probabilities":[{"value":0,"label":"0","probability":0.25},{"value":1,"label":"1","probability":0.75}]}],"usage":{"input_tokens":20,"output_tokens":0,"total_tokens":20}}`
	a := GetAdaptor("openai")
	result, err := a.ParseDecisionResponse([]byte(body), r)
	if err != nil {
		t.Fatal(err)
	}
	if err := dto.ValidateDecisionAnswers(r.Questions, result.Answers); err != nil {
		t.Fatal("legacy answer contract failed after official bridge", err)
	}
	if !bytes.Contains(result.Answers["p"], []byte(`"noul":0.7`)) || !bytes.Contains(result.Answers["s"], []byte("9007199254740993")) || bytes.Contains(result.Answers["s"], []byte(`"name"`)) || result.ProviderUsage.OutputTokenSource != dto.UsageSourceProvider {
		t.Fatal("legacy probability/legend/extension or known-zero output changed")
	}
	oldWrapper, err := a.ParseResponse([]byte(body), r.Questions)
	if err != nil || !reflect.DeepEqual(result.Answers, oldWrapper.Answers) {
		t.Fatal("old ParseResponse compatibility broken", err)
	}
	r = officialDecisionFixture(t, `{"model":"gpt-6-luna-decisions","state":"x","questions":{"p":{"type":"noul","instructions":"q"}}}`)
	refusal, err := a.ParseDecisionResponse([]byte(`{"model":"gpt-6-luna","answers":[{"name":"p","type":"refusal","reason":"unobservable"}],"usage":{"input_tokens":16,"output_tokens":0,"total_tokens":16}}`), r)
	if err != nil {
		t.Fatal("valid official refusal must not trigger retry", err)
	}
	var answer map[string]json.RawMessage
	_ = json.Unmarshal(refusal.Answers["p"], &answer)
	if string(answer["type"]) != `"refusal"` || string(answer["refusal"]) != "true" || string(answer["reason"]) != `"unobservable"` || len(answer["noul"]) != 0 || len(answer["probability"]) != 0 || refusal.ProviderUsage.TotalTokens != 16 {
		t.Fatal("legacy refusal obscured or invented a prediction")
	}
}

func TestOpenAIDecisionRejectsInvalidProviderUsage(t *testing.T) {
	r := officialDecisionFixture(t, "")
	for name, usage := range map[string]string{
		"missing":                  `{}`,
		"null":                     `null`,
		"negative_input":           `{"input_tokens":-1,"output_tokens":0,"total_tokens":0}`,
		"zero_input":               `{"input_tokens":0,"output_tokens":0,"total_tokens":0}`,
		"negative_output":          `{"input_tokens":10,"output_tokens":-1,"total_tokens":9}`,
		"fractional_tokens":        `{"input_tokens":10.5,"output_tokens":0,"total_tokens":10.5}`,
		"missing_total":            `{"input_tokens":10,"output_tokens":0}`,
		"null_output":              `{"input_tokens":10,"output_tokens":null,"total_tokens":10}`,
		"inconsistent_total":       `{"input_tokens":10,"output_tokens":2,"total_tokens":10}`,
		"negative_cache":           `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"input_tokens_details":{"cached_tokens":-1}}`,
		"cache_exceeds_input":      `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"input_tokens_details":{"cache_write_tokens":11}}`,
		"null_cache":               `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"input_tokens_details":{"cached_tokens":null}}`,
		"reasoning_exceeds_output": `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"output_tokens_details":{"reasoning_tokens":1}}`,
		"wrong_details_type":       `{"input_tokens":10,"output_tokens":0,"total_tokens":10,"input_tokens_details":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"model":"gpt-6-luna","answers":[{"name":"off","type":"predicate","probability":1}],"usage":` + usage + `}`
			if _, err := GetAdaptor("openai").ParseDecisionResponse([]byte(body), r); err == nil {
				t.Fatal("accepted invalid financial usage")
			}
		})
	}
}

func TestOpenAIDecisionRejectsWrongProviderIdentityAndAnswers(t *testing.T) {
	r := officialDecisionFixture(t, "")
	good := `{"model":"gpt-6-luna","answers":[{"name":"off","type":"predicate","probability":1}],"usage":{"input_tokens":16,"output_tokens":0,"total_tokens":16}}`
	for name, raw := range map[string]string{
		"chat_model_response":   strings.Replace(good, `"gpt-6-luna"`, `"gpt-6-luna-decisions"`, 1),
		"wrong_name":            strings.Replace(good, `"off"`, `"other"`, 1),
		"missing_answers":       strings.Replace(good, `[{"name":"off","type":"predicate","probability":1}]`, `[]`, 1),
		"wrong_type":            strings.Replace(good, `"predicate"`, `"noul"`, 1),
		"nonfinite_probability": strings.Replace(good, `"probability":1`, `"probability":1e999`, 1),
		"error_envelope":        strings.Replace(good, `"model":`, `"error":{"type":"provider_fault"},"model":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := GetAdaptor("openai").ParseDecisionResponse([]byte(raw), r); err == nil {
				t.Fatal("accepted corrupt provider result")
			}
		})
	}
}
