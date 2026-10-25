package common

import (
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestRelayInfoToStringPrintsOnlyAllowlistedFields(t *testing.T) {
	info := &RelayInfo{
		RequestID:       "req-1",
		TokenId:         7,
		TokenKey:        "sk-lazu-token-secret",
		UserId:          42,
		UserEmail:       "alice.private@example.com",
		OriginModelName: "gpt-test",
		IsStream:        true,
		ChannelMeta: &ChannelMeta{
			ChannelType:       1,
			ChannelId:         9,
			ChannelBaseUrl:    "https://upstream.example/v1?key=url-secret",
			ApiKey:            "upstream-api-secret",
			Organization:      "org-secret",
			UpstreamModelName: "gpt-test-upstream",
			HeadersOverride:   map[string]interface{}{"Authorization": "Bearer header-secret"},
			ChannelSetting:    dto.ChannelSettings{Proxy: "http://user:proxy-secret@proxy.example"},
		},
	}
	got := info.ToString()
	for _, secret := range []string{"token-secret", "alice", "example.com", "url-secret", "upstream.example", "api-secret", "org-secret", "header-secret", "proxy-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("ToString leaked %q: %s", secret, got)
		}
	}
	for _, want := range []string{"user=42", "token=7", "channel=9", `model="gpt-test"`, `upstream_model="gpt-test-upstream"`, "request=req-1", "stream=true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ToString missing %q: %s", want, got)
		}
	}
	if (*RelayInfo)(nil).ToString() != "RelayInfo<nil>" {
		t.Fatal("nil RelayInfo must not panic")
	}
}
