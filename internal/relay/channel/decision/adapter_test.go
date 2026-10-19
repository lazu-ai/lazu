package decision

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestDecisionDialectWireFixtures(t *testing.T) {
	r, err := dto.ParseDecisionRequest([]byte(`{"model":"jev-1.13","state":{"number":9007199254740993},"questions":{"p":{"type":"noul","instructions":"q"}},"question_generation":{"enabled":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ dialect, base, model, path string }{
		{"typesafe", "https://provider.test/v1", "jev-1.13.0", "/v1/systemone"},
		{"openrouter", "https://provider.test/api", "typesafe/jev-1.13", "/api/alpha/decisions"},
		{"cloudflare_gateway", "https://provider.test", "typesafe/jev", "/client/v4/accounts/acct/ai/run"},
		{"cloudflare_workers_ai", "https://provider.test/client/v4", "clef", "/client/v4/accounts/acct/ai/run/@cf/cloudflare/clef"},
	} {
		t.Run(tc.dialect, func(t *testing.T) {
			a := GetAdaptor(tc.dialect)
			req, err := a.BuildRequest(context.Background(), tc.base, "acct", "fixture-key", tc.model, r)
			if err != nil {
				t.Fatal(err)
			}
			if req.URL.Path != tc.path || req.Header.Get("Authorization") != "Bearer fixture-key" {
				t.Fatalf("unexpected request %v", req.URL)
			}
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), "9007199254740993") || strings.Contains(string(body), "question_generation") {
				t.Fatalf("business JSON or controls changed: %s", body)
			}
			var decoded map[string]json.RawMessage
			_ = json.Unmarshal(body, &decoded)
			if len(decoded["model"]) == 0 {
				t.Fatal("model omitted")
			}
			response := `{"model":"` + tc.model + `","answers":{"p":{"type":"noul","noul":0.6,"extension":1}},"usage":{"input_tokens":457,"output_tokens":70}}`
			if tc.dialect == "cloudflare_gateway" {
				response = `{"state":"Completed","result":` + response + `}`
			}
			if strings.HasPrefix(tc.dialect, "cloudflare_") {
				response = `{"success":true,"errors":[],"result":` + response + `}`
			}
			result, err := a.ParseResponse([]byte(response), r.Questions)
			if err != nil {
				t.Fatal(err)
			}
			if result.Usage.OutputTokens != 70 || !strings.Contains(string(result.Answers["p"]), "extension") {
				t.Fatal("response usage or extension dropped")
			}
		})
	}
}

func TestDecisionCloudflareEnvelopeFailure(t *testing.T) {
	a := GetAdaptor("cloudflare_workers_ai")
	for _, body := range []string{`{"success":false,"errors":[{"code":3040}]}`, `{"result":{}}`, `{"success":true,"result":{"model":"clef","answers":{}}}`} {
		if _, err := a.ParseResponse([]byte(body), map[string]json.RawMessage{"p": json.RawMessage(`{"type":"noul","instructions":"q"}`)}); err == nil {
			t.Fatal("accepted", body)
		}
	}
}

func TestDecisionEncodedBodyLimit(t *testing.T) {
	r := &dto.DecisionRequest{State: json.RawMessage(`"` + strings.Repeat("x", dto.DecisionMaxBodyBytes) + `"`), Questions: map[string]json.RawMessage{"q": json.RawMessage(`{"type":"noul","instructions":"q"}`)}}
	if _, err := GetAdaptor("typesafe").BuildRequest(context.Background(), "https://example.test", "", "k", "jev-1.13.0", r); err == nil {
		t.Fatal("accepted oversized body")
	}
}
