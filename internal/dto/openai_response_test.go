package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIncompleteDetailsParsesStandardReason(t *testing.T) {
	var event ResponsesStreamResponse
	if err := json.Unmarshal([]byte(`{
		"type":"response.incomplete",
		"response":{
			"status":"incomplete",
			"incomplete_details":{"reason":"max_output_tokens"}
		}
	}`), &event); err != nil {
		t.Fatalf("unmarshal response.incomplete: %v", err)
	}
	if event.Response == nil || event.Response.IncompleteDetails == nil {
		t.Fatalf("expected incomplete details, got %+v", event.Response)
	}
	if event.Response.IncompleteDetails.Reason != "max_output_tokens" {
		t.Fatalf("unexpected incomplete reason: %+v", event.Response.IncompleteDetails)
	}
}

func TestInputTokenDetailsPreservesCachedModalitySplit(t *testing.T) {
	var details InputTokenDetails
	if err := json.Unmarshal([]byte(`{
		"cached_tokens":20,
		"cached_tokens_details":{"text_tokens":12,"audio_tokens":6,"image_tokens":2},
		"text_tokens":80,
		"audio_tokens":20
	}`), &details); err != nil {
		t.Fatalf("unmarshal input token details: %v", err)
	}
	if details.CachedTokensDetails.TextTokens != 12 ||
		details.CachedTokensDetails.AudioTokens != 6 ||
		details.CachedTokensDetails.ImageTokens != 2 {
		t.Fatalf("cached modality details were lost: %+v", details)
	}

	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal input token details: %v", err)
	}
	if !strings.Contains(string(encoded), `"cached_tokens_details":{"text_tokens":12,"audio_tokens":6,"image_tokens":2}`) {
		t.Fatalf("cached modality details missing from marshaled usage: %s", encoded)
	}
}
