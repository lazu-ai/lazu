package dto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"
)

func jevPNG(t *testing.T, size int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
func assetRequest(t *testing.T) DecisionRequest {
	return DecisionRequest{Model: "jev-1.14", State: json.RawMessage(`{"n":9007199254740993,"photo":"ref-photo"}`), Assets: map[string]DecisionAsset{
		"ref-photo":  {Format: "png", DataBase64: jevPNG(t, 280)},
		"ref-option": {Type: "image", Format: "png", DataBase64: jevPNG(t, 280)},
	}, Questions: map[string]json.RawMessage{"q": json.RawMessage(`{"type":"choice","instructions":"Compare ref-photo", "criteria":{"match":{"sample":"ref-option"},"other":null}}`)}}
}
func TestJevAssetsPreserveBusinessJSON(t *testing.T) {
	request := assetRequest(t)
	body, _ := json.Marshal(request)
	r, err := ParseDecisionRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.ImageInfo) != 2 || !strings.Contains(string(r.State), "9007199254740993") {
		t.Fatal("assets or state lost")
	}
	if err := r.Validate(true); err != nil {
		t.Fatal(err)
	}
	state, assets, err := r.JevImagePayload()
	if err != nil || string(state) != string(request.State) || len(assets) != 2 {
		t.Fatalf("changed original references: %s %v", state, err)
	}
	meta := r.GetTokenCountMeta()
	if meta.AdmissionTokenFloor != 65536 || strings.Contains(meta.CombineText, r.Assets["ref-photo"].DataBase64) {
		t.Fatal("image bytes counted as text or reserve missing")
	}
}
func TestJevAssetsRejectInvalidInputs(t *testing.T) {
	for _, name := range []string{"other-model", "unused", "key-only", "too-small", "too-large", "unknown-type", "webp", "mixed", "no-questions", "bad-ref", "corrupt", "expanded", "too-many"} {
		t.Run(name, func(t *testing.T) {
			r := assetRequest(t)
			switch name {
			case "other-model":
				r.Model = "clef"
			case "unused":
				r.Assets["unused"] = r.Assets["ref-photo"]
			case "key-only":
				r.State = json.RawMessage(`{"ref-photo":42}`)
				r.Questions["q"] = json.RawMessage(`{"type":"noul","instructions":"ref-option"}`)
			case "too-small":
				r.Assets["ref-photo"] = DecisionAsset{Format: "png", DataBase64: jevPNG(t, 279)}
			case "too-large":
				r.Assets["ref-photo"] = DecisionAsset{Format: "png", DataBase64: jevPNG(t, 1569)}
			case "unknown-type":
				r.Assets["ref-photo"] = DecisionAsset{Type: "video", Format: "png", DataBase64: jevPNG(t, 280)}
			case "webp":
				r.Assets["ref-photo"] = DecisionAsset{Format: "webp", DataBase64: jevPNG(t, 280)}
			case "mixed":
				r.Images = []json.RawMessage{json.RawMessage(`"data:image/png;base64,` + jevPNG(t, 280) + `"`)}
			case "no-questions":
				r.Questions = nil
			case "bad-ref":
				r.Assets[" "] = r.Assets["ref-photo"]
			case "corrupt":
				r.Assets["ref-photo"] = DecisionAsset{Format: "png", DataBase64: "AAAA"}
			case "expanded":
				r.State, _ = json.Marshal(strings.Repeat("ref-photo ", 50000))
			case "too-many":
				for _, key := range []string{"a", "b", "c"} {
					r.Assets[key] = r.Assets["ref-photo"]
				}
			}
			body, _ := json.Marshal(r)
			if _, err := ParseDecisionRequest(body); err == nil {
				t.Fatal("accepted invalid assets")
			}
		})
	}
	r := assetRequest(t)
	body, _ := json.Marshal(r)
	for _, field := range []string{`"part":"upload",`, `"url":"https://example.test/x",`} {
		bad := bytes.Replace(body, []byte(`"format":"png"`), []byte(field+`"format":"png"`), 1)
		if _, err := ParseDecisionRequest(bad); err == nil {
			t.Fatal("accepted unsupported asset field")
		}
	}
}
func TestJevSharedImagesDoNotMutateRequest(t *testing.T) {
	raw, _ := json.Marshal("data:image/png;base64," + jevPNG(t, 280))
	r := &DecisionRequest{Model: "jev-1.14", State: json.RawMessage(`{"n":9007199254740993}`), Images: []json.RawMessage{raw}, Questions: map[string]json.RawMessage{"q": json.RawMessage(`{"type":"noul","instructions":"red?"}`)}}
	if err := r.Validate(true); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(r)
	for i := 0; i < 2; i++ {
		state, assets, err := r.JevImagePayload()
		if err != nil || len(assets) != 1 || !strings.Contains(string(state), "9007199254740993") {
			t.Fatal("bad shared image payload", err)
		}
		for key := range assets {
			if !strings.Contains(string(state), strings.ReplaceAll(strings.ReplaceAll(key, "<", `\u003c`), ">", `\u003e`)) {
				t.Fatal("reference missing", string(state), key)
			}
		}
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("payload conversion mutated request")
	}
}

func TestAssetReferencesInDeepJSONAndEscapedStrings(t *testing.T) {
	refs := map[string]int{"image-ref": 0, "key-only": 0}
	raw := strings.Repeat("[", 1000) + `{"key-only":["image-ref","look at image-ref","image\u002dref",9007199254740993]}` + strings.Repeat("]", 1000)
	countAssetReferences(json.RawMessage(raw), refs)
	if refs["image-ref"] != 3 || refs["key-only"] != 0 {
		t.Fatalf("reference walk = %v", refs)
	}
}
