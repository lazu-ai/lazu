package helper

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestValidateOutboundResponsesStore(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantErr     bool
		wantErrHint string
	}{
		{
			name: "store omitted with previous_response_id is allowed",
			body: `{"model":"gpt-5.5","input":"hi","previous_response_id":"resp_123"}`,
		},
		{
			name: "store true with previous_response_id is allowed",
			body: `{"model":"gpt-5.5","input":"hi","store":true,"previous_response_id":"resp_123"}`,
		},
		{
			name: "store null with previous_response_id is allowed",
			body: `{"model":"gpt-5.5","input":"hi","store":null,"previous_response_id":"resp_123"}`,
		},
		{
			name: "store false alone is allowed",
			body: `{"model":"gpt-5.5","input":"hi","store":false}`,
		},
		{
			name:    "store false with previous_response_id is rejected",
			body:    `{"model":"gpt-5.5","input":"hi","store":false,"previous_response_id":"resp_123"}`,
			wantErr: true, wantErrHint: "previous_response_id",
		},
		{
			name:    "store false with item_reference is rejected",
			body:    `{"model":"gpt-5.5","store":false,"input":[{"type":"item_reference","id":"rs_abc"}]}`,
			wantErr: true, wantErrHint: "item_reference",
		},
		{
			// The exact shape behind the upstream error
			// "Item with id 'rs_...' not found."
			name:    "store false with bare reasoning item is rejected",
			body:    `{"model":"gpt-5.5","store":false,"input":[{"type":"reasoning","id":"rs_abc","summary":[]}]}`,
			wantErr: true, wantErrHint: "encrypted_content",
		},
		{
			// The supported ZDR pattern: the reasoning item carries its own
			// state, so no server-side lookup is needed. Must not regress.
			name: "store false with encrypted reasoning item is allowed",
			body: `{"model":"gpt-5.5","store":false,"include":["reasoning.encrypted_content"],` +
				`"input":[{"type":"reasoning","id":"rs_abc","summary":[],"encrypted_content":"gAAAAA"},{"role":"user","content":"hi"}]}`,
		},
		{
			name: "store false with plain message items is allowed",
			body: `{"model":"gpt-5.5","store":false,"input":[{"role":"user","content":"hi"},` +
				`{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"yo"}]}]}`,
		},
		{
			name: "store false with empty reasoning id is allowed",
			body: `{"model":"gpt-5.5","store":false,"input":[{"type":"reasoning","summary":[]}]}`,
		},
		{
			name: "unparseable body is left to the provider",
			body: `not json`,
		},
		{
			name: "empty body is left to the provider",
			body: ``,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOutboundResponsesStore([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantErrHint) {
					t.Fatalf("error %q does not mention %q", err.Error(), tc.wantErrHint)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// TestValidateOutboundResponsesStoreAfterChannelPolicy runs the real channel
// pipeline that decides the outbound `store` value, because that pipeline —
// not the client's request — is what the check has to agree with.
func TestValidateOutboundResponsesStoreAfterChannelPolicy(t *testing.T) {
	clientBody := []byte(`{"model":"gpt-5.5","input":"hi","store":false,"previous_response_id":"resp_123"}`)

	t.Run("disable_store channel makes server-side state usable again", func(t *testing.T) {
		// The channel strips `store`, so upstream defaults it to true and can
		// resolve previous_response_id. Rejecting this would be a regression.
		outbound, err := relaycommon.RemoveDisabledFields(clientBody, dto.ChannelOtherSettings{DisableStore: true}, false)
		if err != nil {
			t.Fatalf("RemoveDisabledFields: %v", err)
		}
		if strings.Contains(string(outbound), "store") {
			t.Fatalf("precondition failed: store should have been stripped, got %s", outbound)
		}
		if err := ValidateOutboundResponsesStore(outbound); err != nil {
			t.Fatalf("disable_store channel must still accept previous_response_id, got %v", err)
		}
	})

	t.Run("default channel keeps store false and is rejected", func(t *testing.T) {
		outbound, err := relaycommon.RemoveDisabledFields(clientBody, dto.ChannelOtherSettings{}, false)
		if err != nil {
			t.Fatalf("RemoveDisabledFields: %v", err)
		}
		if err := ValidateOutboundResponsesStore(outbound); err == nil {
			t.Fatal("expected the store conflict to be rejected")
		}
	})

	t.Run("param override forcing store false is caught", func(t *testing.T) {
		// The client never sent `store`; the operator's override adds it, so
		// the conflict only exists in the outbound body.
		compliantBody := []byte(`{"model":"gpt-5.5","input":"hi","previous_response_id":"resp_123"}`)
		if err := ValidateOutboundResponsesStore(compliantBody); err != nil {
			t.Fatalf("precondition failed: client body should be valid, got %v", err)
		}
		outbound, err := relaycommon.ApplyParamOverride(compliantBody, map[string]any{"store": false})
		if err != nil {
			t.Fatalf("ApplyParamOverride: %v", err)
		}
		if err := ValidateOutboundResponsesStore(outbound); err == nil {
			t.Fatal("expected the override-introduced store conflict to be rejected")
		}
	})
}
