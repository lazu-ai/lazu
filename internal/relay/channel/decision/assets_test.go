package decision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestJevAssetsWireAndUsage(t *testing.T) {
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 280, 280))); err != nil {
		t.Fatal(err)
	}
	raw := `{"model":"jev-1.14","state":{"photo":"photo-ref","n":9007199254740993},"assets":{"photo-ref":{"format":"png","data_base64":"` + base64.StdEncoding.EncodeToString(picture.Bytes()) + `"}},"questions":{"q":{"type":"noul","instructions":"photo-ref contains red?"}}}`
	r, err := dto.ParseDecisionRequest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(r)
	for _, dialect := range []string{"typesafe", "openrouter", "cloudflare_gateway", "cloudflare_workers_ai", "openai"} {
		t.Run(dialect, func(t *testing.T) {
			a := GetAdaptor(dialect)
			req, err := a.BuildRequest(context.Background(), "https://provider.test", "fixture", "key", "jev-1.14.0", r)
			if dialect != "typesafe" && dialect != "openrouter" {
				if err == nil {
					t.Fatal("unsupported provider accepted assets")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(req.Body)
			var payload struct {
				State     json.RawMessage              `json:"state"`
				Assets    map[string]dto.DecisionAsset `json:"assets"`
				Questions map[string]json.RawMessage   `json:"questions"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			if string(payload.State) != string(r.State) || string(payload.Questions["q"]) != string(r.Questions["q"]) || payload.Assets["photo-ref"].DataBase64 != r.Assets["photo-ref"].DataBase64 {
				t.Fatal("payload lost image semantics")
			}
			wantHeader := ""
			if dialect == "typesafe" {
				wantHeader = "true"
			}
			if req.Header.Get("TypeSafe-Danger-Zone-Images") != wantHeader {
				t.Fatal("wrong opt-in header")
			}
			for _, usage := range []string{"", `,"usage":{"output_tokens":0}`, `,"usage":{"input_tokens":-1}`, `,"usage":{"input_tokens":417,"output_tokens":0}`} {
				response := `{"model":"jev-1.14.0","answers":{"q":{"type":"noul","noul":0.9}}` + usage + `}`
				got, err := a.ParseDecisionResponse([]byte(response), r)
				if usage == `,"usage":{"input_tokens":417,"output_tokens":0}` {
					if err != nil || got.Usage.InputTokens != 417 {
						t.Fatal("usage lost", err)
					}
				} else if err == nil {
					t.Fatal("missing/invalid image usage accepted")
				}
			}
		})
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(original, after) {
		t.Fatal("attempt mutated input")
	}
}
