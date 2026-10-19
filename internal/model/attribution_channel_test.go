package model

import (
	"testing"
)

func TestAttributionChannel(t *testing.T) {
	cases := []struct {
		source, referrer, referral, invite, want string
	}{
		{"chatgpt.com", "", "", "", AttributionChannelAISearch},
		{"", "chatgpt.com", "", "", AttributionChannelAISearch},
		{"ChatGPT.com", "", "", "", AttributionChannelAISearch},
		{"perplexity", "", "", "", AttributionChannelAISearch},
		{"", "www.perplexity.ai", "", "", AttributionChannelAISearch},
		{"", "gemini.google.com", "", "", AttributionChannelAISearch},
		{"", "www.google.com.hk", "", "", AttributionChannelSearch},
		{"", "google.co.jp", "", "", AttributionChannelSearch},
		{"", "cn.bing.com", "", "", AttributionChannelSearch},
		{"", "linux.do", "", "", AttributionChannelCommunity},
		{"reddit", "reddit.com", "", "", AttributionChannelCommunity},
		{"youtube", "", "", "", AttributionChannelCommunity},
		{"github", "", "", "", AttributionChannelCommunity},
		{"linkedin", "", "", "", AttributionChannelCommunity},
		{"xiaohongshu", "", "", "", AttributionChannelCommunity},
		{"grace", "", "grace", "", AttributionChannelReferral},
		{"", "", "grace", "", AttributionChannelReferral},
		{"", "", "", "team-invite", AttributionChannelInvite},
		{"", "", "", "", AttributionChannelDirect},
		{"newsletter-x", "", "", "", AttributionChannelOther},
		// An explicit utm source outranks the referral cookie.
		{"chatgpt.com", "", "grace", "", AttributionChannelAISearch},
		// A look-alike is not the engine.
		{"", "notchatgpt.com", "", "", AttributionChannelOther},
	}
	for _, c := range cases {
		if got := AttributionChannel(c.source, c.referrer, c.referral, c.invite); got != c.want {
			t.Errorf("AttributionChannel(%q,%q,%q,%q)=%q want %q", c.source, c.referrer, c.referral, c.invite, got, c.want)
		}
	}
}
