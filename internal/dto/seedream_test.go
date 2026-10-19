package dto

import (
	"encoding/json"
	"testing"
)

func TestSeedreamRequestValidation(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		valid      bool
	}{
		{"lite default", `{"model":"seedream-5.0-lite","prompt":"cube"}`, true},
		{"lite invalid legacy size", `{"model":"seedream-5.0-lite","prompt":"cube","size":"1024x1024"}`, false},
		{"lite batch", `{"model":"seedream-5.0-lite","prompt":"cubes","n":2}`, true},
		{"huge batch", `{"model":"seedream-5.0-lite","prompt":"cubes","n":99}`, false},
		{"conflicting batch", `{"model":"seedream-5.0-lite","prompt":"cubes","n":2,"sequential_image_generation_options":{"max_images":15}}`, false},
		{"too many refs and outputs", `{"model":"seedream-5.0-lite","prompt":"cube","image":["a","b"],"sequential_image_generation":"auto","sequential_image_generation_options":{"max_images":14}}`, false},
		{"pro no batches", `{"model":"seedream-5.0-pro","prompt":"cube","n":2}`, false},
		{"pro layers without prompt", `{"model":"seedream-5.0-pro","image":"a","layer_decomposition":true}`, true},
		{"layers need reference", `{"model":"seedream-5.0-flash","layer_decomposition":true}`, false},
		{"layers reject multiple refs", `{"model":"seedream-5.0-pro","image":["a","b"],"layer_decomposition":true}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var r ImageRequest
			if err := json.Unmarshal([]byte(tt.body), &r); err != nil {
				t.Fatal(err)
			}
			err := r.PrepareSeedream()
			if (err == nil) != tt.valid {
				t.Fatalf("error=%v", err)
			}
			if err == nil && len(r.SeedreamReservation().Outputs) == 0 {
				t.Fatal("missing admission reservation")
			}
		})
	}
}

func TestSeedreamNativeParametersSurviveCopy(t *testing.T) {
	var request ImageRequest
	if err := json.Unmarshal([]byte(`{"model":"dola-seedream-5-0-pro-260628","prompt":"edit","image":["a"],"layer_decomposition":true,"optimize_prompt_options":{"mode":"fast"},"seed":7,"watermark":false}`), &request); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var copied map[string]json.RawMessage
	_ = json.Unmarshal(body, &copied)
	for _, key := range []string{"image", "layer_decomposition", "optimize_prompt_options", "seed", "watermark"} {
		if _, ok := copied[key]; !ok {
			t.Fatalf("lost %s", key)
		}
	}
}
