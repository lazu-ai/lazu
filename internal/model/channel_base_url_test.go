package model

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/constant"
)

func TestChannelGetBaseURL(t *testing.T) {
	empty, custom := "", "http://localhost:8080"
	for _, tt := range []struct {
		name        string
		channelType int
		baseURL     *string
		want        string
	}{
		{"nil uses provider default", constant.ChannelTypeOpenAI, nil, "https://api.openai.com"},
		{"empty uses provider default", constant.ChannelTypeOpenAI, &empty, "https://api.openai.com"},
		{"explicit override", constant.ChannelTypeOpenAI, &custom, custom},
		{"custom has no invented default", constant.ChannelTypeCustom, nil, ""},
		{"negative type", -1, &empty, ""},
		{"unknown type", len(constant.ChannelBaseURLs), nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ch := Channel{Type: tt.channelType, BaseURL: tt.baseURL}
			if got := ch.GetBaseURL(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
