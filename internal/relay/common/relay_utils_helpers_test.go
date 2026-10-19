package common

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
)

func TestGetFullRequestURL(t *testing.T) {
	t.Parallel()

	base := "https://api.example.com"
	req := "/v1/chat/completions"
	if got := GetFullRequestURL(base, req, constant.ChannelTypeOpenAI); got != "https://api.example.com/v1/chat/completions" {
		t.Fatalf("unexpected default url: %s", got)
	}

	cfBase := "https://gateway.ai.cloudflare.com/account/gateway/openai"
	if got := GetFullRequestURL(cfBase, "/v1/chat/completions", constant.ChannelTypeOpenAI); got != "https://gateway.ai.cloudflare.com/account/gateway/openai/chat/completions" {
		t.Fatalf("unexpected cloudflare openai url: %s", got)
	}

}
