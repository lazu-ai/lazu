package telegram

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
)

// Answers may only link to this deployment's own pages. The rule is in the
// prompt too, but a model that is talked into a phishing link must not get
// it into the chat, so links are checked here as well: a Markdown link to
// any other host keeps its text and loses its target, and a bare URL to any
// other host is dropped (Telegram would make it clickable). Code is left
// alone; Telegram does not link inside it.

var bareURL = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)

// allowedLink reports whether u points at this deployment: the web or API
// origin or one of their subdomains, a knowledge source's host, or a page
// under the published source URL.
func allowedLink(raw string) bool {
	u, err := url.Parse(strings.TrimRight(raw, ".,;:!?。，；："))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if src := common.SiteSourceURL(); src != "" && strings.HasPrefix(raw, src) {
		return true
	}
	for _, base := range ownHosts() {
		if host == base || strings.HasSuffix(host, "."+base) {
			return true
		}
	}
	return false
}

func ownHosts() []string {
	var hosts []string
	add := func(raw string) {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			hosts = append(hosts, strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."))
		}
	}
	add(common.SiteWebOrigin())
	add(common.SiteAPIOrigin())
	for _, k := range KnowledgeURLs() {
		add(k)
	}
	return hosts
}

// KeepOwnLinks removes links to other hosts from Markdown prose.
func KeepOwnLinks(md string) string {
	var out strings.Builder
	last := 0
	for _, loc := range fencePattern.FindAllStringIndex(md, -1) {
		out.WriteString(keepOwnLinksInProse(md[last:loc[0]]))
		out.WriteString(md[loc[0]:loc[1]])
		last = loc[1]
	}
	out.WriteString(keepOwnLinksInProse(md[last:]))
	return out.String()
}

func keepOwnLinksInProse(s string) string {
	// Inline code is kept as it is.
	var codes []string
	s = inlineCode.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, m)
		return "\x02" + string(rune(len(codes)-1+0x100)) + "\x02"
	})
	var kept []string
	s = linkPattern.ReplaceAllStringFunc(s, func(m string) string {
		parts := linkPattern.FindStringSubmatch(m)
		if allowedLink(parts[2]) {
			kept = append(kept, m)
			return "\x03" + string(rune(len(kept)-1+0x100)) + "\x03"
		}
		return parts[1]
	})
	s = bareURL.ReplaceAllStringFunc(s, func(m string) string {
		if allowedLink(m) {
			return m
		}
		return ""
	})
	for i, m := range kept {
		s = strings.Replace(s, "\x03"+string(rune(i+0x100))+"\x03", m, 1)
	}
	for i, m := range codes {
		s = strings.Replace(s, "\x02"+string(rune(i+0x100))+"\x02", m, 1)
	}
	return s
}
