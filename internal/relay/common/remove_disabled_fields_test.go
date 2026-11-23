package common

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestRemoveDisabledFieldsDefaultFiltering(t *testing.T) {
	input := []byte(`{
		"service_tier":"flex",
		"speed":{"mode":"fast"},
		"inference_geo":"eu",
		"safety_identifier":"user-123",
		"store":true,
		"stream_options":{"include_usage":true,"include_obfuscation":false}
	}`)

	got, err := RemoveDisabledFields(input, dto.ChannelOtherSettings{})
	if err != nil {
		t.Fatalf("RemoveDisabledFields returned error: %v", err)
	}

	assertJSONEqual(t, got, []byte(`{"store":true,"stream_options":{"include_usage":true}}`))
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotData map[string]any
	var wantData map[string]any

	if err := json.Unmarshal(got, &gotData); err != nil {
		t.Fatalf("failed to unmarshal got json: %v", err)
	}
	if err := json.Unmarshal(want, &wantData); err != nil {
		t.Fatalf("failed to unmarshal want json: %v", err)
	}
	if !reflect.DeepEqual(gotData, wantData) {
		t.Fatalf("unexpected json: got %s want %s", string(got), string(want))
	}
}
