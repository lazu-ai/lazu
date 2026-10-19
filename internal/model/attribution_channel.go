package model

import (
	"strings"
)

// Acquisition channels. A channel is derived when attribution is read, never
// stored, so the host lists below can grow without a backfill.
const (
	AttributionChannelAISearch  = "ai_search"
	AttributionChannelSearch    = "search"
	AttributionChannelCommunity = "community"
	AttributionChannelReferral  = "referral"
	AttributionChannelInvite    = "invite"
	AttributionChannelDirect    = "direct"
	AttributionChannelOther     = "other"
)

// Answer engines append their own utm_source (ChatGPT sends chatgpt.com) and
// rarely send a referrer, so both the bare name and the host are listed.
// Unknown values fall to "other"; the raw source stays visible beside the
// channel so a new engine shows up before it is added here.
var attributionChannelHosts = map[string][]string{
	AttributionChannelAISearch: {
		"chatgpt.com", "chat.openai.com", "openai.com", "chatgpt", "openai",
		"perplexity.ai", "perplexity",
		"gemini.google.com", "bard.google.com", "gemini",
		"claude.ai", "claude",
		"copilot.microsoft.com", "copilot.cloud.microsoft", "copilot",
		"grok.com", "grok",
		"you.com", "phind.com", "poe.com", "meta.ai",
		"chat.deepseek.com", "kimi.com", "kimi.moonshot.cn", "doubao.com",
		"yuanbao.tencent.com", "metaso.cn", "tongyi.aliyun.com", "chat.qwen.ai",
	},
	AttributionChannelSearch: {
		"google", "bing.com", "bing", "baidu.com", "baidu", "duckduckgo.com",
		"yandex", "sogou.com", "so.com", "search.yahoo.com", "naver.com",
		"ecosia.org", "search.brave.com",
	},
	AttributionChannelCommunity: {
		"x.com", "twitter.com", "t.co", "reddit.com", "news.ycombinator.com",
		"linkedin.com", "lnkd.in", "youtube.com", "github.com", "producthunt.com",
		"xiaohongshu.com", "xhslink.com", "zhihu.com", "weibo.com", "weibo.cn",
		"bilibili.com", "linux.do", "v2ex.com", "juejin.cn", "jike.city",
		"okjike.com", "discord.com", "t.me", "telegram.org",
		"reddit", "youtube", "github", "linkedin", "x", "twitter", "hackernews", "producthunt",
		"community", "social", "xiaohongshu", "zhihu", "linuxdo", "v2ex",
	},
}

// AttributionChannel groups a landing or a registration. The utm source wins
// over the referring host; with neither, a referral or invite code decides;
// with nothing at all the visit was direct.
func AttributionChannel(source, referrerHost, referralCode, inviteCode string) string {
	for _, raw := range []string{source, referrerHost} {
		value := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "www.")
		if value == "" {
			continue
		}
		if channel := channelForValue(value); channel != "" {
			return channel
		}
		if value == strings.ToLower(strings.TrimSpace(referralCode)) {
			// The web collector files a bare ?ref= landing under its code.
			return AttributionChannelReferral
		}
		return AttributionChannelOther
	}
	if strings.TrimSpace(referralCode) != "" {
		return AttributionChannelReferral
	}
	if strings.TrimSpace(inviteCode) != "" {
		return AttributionChannelInvite
	}
	return AttributionChannelDirect
}

func channelForValue(value string) string {
	for _, channel := range []string{AttributionChannelAISearch, AttributionChannelSearch, AttributionChannelCommunity} {
		for _, host := range attributionChannelHosts[channel] {
			if value == host || strings.HasSuffix(value, "."+host) {
				return channel
			}
			// google.com, google.co.jp, google.com.hk …
			if !strings.Contains(host, ".") && strings.HasPrefix(value, host+".") {
				return channel
			}
		}
	}
	return ""
}
