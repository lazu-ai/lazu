package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestWithInternalUserIDsPreservesPublicAndInternalIdentifiers(t *testing.T) {
	accessToken := "secret-admin-token"
	items := withInternalUserIDs([]*model.User{
		{
			Id:          42,
			PublicID:    "usr_public",
			Username:    "member",
			Password:    "secret-password",
			AccessToken: &accessToken,
			Setting:     `{"private":true}`,
		},
	})

	if len(items) != 1 || items[0].InternalID != 42 {
		t.Fatalf("expected internal user ID 42, got %#v", items)
	}

	encoded, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal admin user item: %v", err)
	}
	payload := string(encoded)
	if !strings.Contains(payload, `"id":"usr_public"`) {
		t.Fatalf("expected public ID in payload, got %s", payload)
	}
	if !strings.Contains(payload, `"internal_id":42`) {
		t.Fatalf("expected internal ID in payload, got %s", payload)
	}
	for _, secret := range []string{"secret-password", "secret-admin-token", `\"private\"`, "cus_secret", "access_token", "stripe_customer", "setting"} {
		if strings.Contains(payload, secret) {
			t.Fatalf("safe admin projection leaked %q in %s", secret, payload)
		}
	}
}
