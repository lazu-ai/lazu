package dto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func decisionFixture(t *testing.T, patch string) *DecisionRequest {
	t.Helper()
	body := `{"model":"jev-1.13","state":{"ticket":"支付失败","number":9007199254740993},"questions":{"ok":{"type":"noul","instructions":{"question":"是否失败？","reference_data":[1,2]}}}}`
	if patch != "" {
		body = patch
	}
	r, err := ParseDecisionRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDecisionRawBusinessJSONPreserved(t *testing.T) {
	r := decisionFixture(t, "")
	if string(r.State) != `{"ticket":"支付失败","number":9007199254740993}` {
		t.Fatal("state changed:", string(r.State))
	}
	if !strings.Contains(string(r.Questions["ok"]), `"reference_data":[1,2]`) {
		t.Fatal("question reference changed")
	}
	if err := r.Validate(true); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionRejectsInvalidInputs(t *testing.T) {
	cases := []string{
		`[]`, `null`, `{"model":"jev-1.13"}`, `{"model":"jev-1.13","state":null}`,
		`{"model":"jev-1.13","state":1}`, `{"model":"jev-1.13","state":{}}`,
		`{"model":"jev-1.13","state":"hello","stream":true}`,
		`{"model":"jev-1.13","state":"hello","temperature":0}`,
		`{"model":"jev-1.13","state":"hello","videos":[]}`,
		`{"model":"jev-1.13","state":"hello","images":["https://example.com/image.png"]}`,
		`{"model":"clef","state":"hello","images":[{"file_id":"file_a"}]}`,
		`{"model":"clef","state":"hello","images":["data:image/png;base64,AAAA"]}`,
		`{"model":"jev-1.13","state":"hello","question_generation":{"enabled":true,"tools":[]}}`,
		`{"model":"jev-1.13","state":"hello","questions":{"bad id":{"type":"noul","instructions":"q"}}}`,
		`{"model":"jev-1.13","state":"hello","questions":{"q":{"type":"choice","instructions":"q","criteria":{"a":null}}}}`,
		`{"model":"jev-1.13","state":"hello","questions":{"q":{"type":"score","instructions":"q","criteria":["one"]}}}`,
		`{"model":"jev-1.13","state":"hello","questions":{"q":{"type":"multi","instructions":"q"}}}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			if _, err := ParseDecisionRequest([]byte(body)); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func decisionPNG(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestDecisionImageOnlyAndCanonicalImageConversion(t *testing.T) {
	url := decisionPNG(t)
	body, _ := json.Marshal(map[string]interface{}{"model": "clef", "state": "", "images": []interface{}{url, map[string]string{"url": url}}, "questions": map[string]interface{}{"q": map[string]string{"type": "noul", "instructions": "white?"}}})
	r := decisionFixture(t, string(body))
	if len(r.ImageInfo) != 2 || r.ImageInfo[0].DataURL != url || r.ImageInfo[0].SHA256 != r.ImageInfo[1].SHA256 || r.ImageInfo[0].Bytes == 0 {
		t.Fatal("image conversion failed")
	}
	if _, err := ParseDecisionImage(json.RawMessage(`"` + strings.Replace(url, "image/png", "image/jpeg", 1) + `"`)); err == nil {
		t.Fatal("accepted mismatched content type")
	}
}

func TestDecisionAnswersValidateCompleteProbabilityMaps(t *testing.T) {
	q := map[string]json.RawMessage{
		"p": json.RawMessage(`{"type":"noul","instructions":"q"}`),
		"c": json.RawMessage(`{"type":"choice","instructions":"q","criteria":{"a":null,"b":"B"}}`),
		"s": json.RawMessage(`{"type":"score","instructions":"q","criteria":["low",{"description":"high"}]}`),
	}
	a := map[string]json.RawMessage{
		"p": json.RawMessage(`{"type":"noul","noul":0.5}`),
		"c": json.RawMessage(`{"type":"choice","choice":"a","probabilities":{"a":0.6,"b":0.4},"new_provider_field":{"x":1}}`),
		"s": json.RawMessage(`{"type":"score","score":0.8,"probabilities":{"0":0.2,"1":0.8},"legend":{"0":"low","1":{"description":"high"}}}`),
	}
	if err := ValidateDecisionAnswers(q, a); err != nil {
		t.Fatal(err)
	}
	original := a["c"]
	for _, bad := range []string{
		`{"type":"choice","choice":"z","probabilities":{"a":0.6,"b":0.4}}`,
		`{"type":"choice","choice":"a","probabilities":{"a":1.1,"b":-0.1}}`,
		`{"type":"choice","choice":"a","probabilities":{"a":0.6}}`,
		`{"type":"choice","choice":"a","probabilities":{"a":0.6,"b":0.8}}`,
		`{"type":"noul","noul":0.8}`,
	} {
		a["c"] = json.RawMessage(bad)
		if err := ValidateDecisionAnswers(q, a); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	a["c"] = original
	delete(a, "p")
	if err := ValidateDecisionAnswers(q, a); err == nil {
		t.Fatal("accepted missing answer")
	}
}
