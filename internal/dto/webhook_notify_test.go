package dto

import "testing"

func TestWebhookNotifyPayloadValidate(t *testing.T) {
	var nilPayload *WebhookNotifyPayload
	if err := nilPayload.Validate(); err == nil {
		t.Fatal("expected nil payload error")
	}

	tests := []struct {
		name    string
		payload WebhookNotifyPayload
		wantErr bool
	}{
		{name: "missing type", payload: WebhookNotifyPayload{Title: "t", Content: "c", Timestamp: 1}, wantErr: true},
		{name: "missing title", payload: WebhookNotifyPayload{Type: "quota_exceed", Content: "c", Timestamp: 1}, wantErr: true},
		{name: "missing content", payload: WebhookNotifyPayload{Type: "quota_exceed", Title: "t", Timestamp: 1}, wantErr: true},
		{name: "invalid timestamp", payload: WebhookNotifyPayload{Type: "quota_exceed", Title: "t", Content: "c"}, wantErr: true},
		{name: "valid payload", payload: WebhookNotifyPayload{Type: "quota_exceed", Title: "quota warning", Content: "quota low", Timestamp: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.payload.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}
