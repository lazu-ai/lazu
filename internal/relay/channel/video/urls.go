package video

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const cloudflareGatewayPrefix = "https://gateway.ai.cloudflare.com"

// joinPath appends a provider path to the channel base URL. An OpenAI-type
// channel behind the Cloudflare AI gateway already carries /v1 in its base.
func joinPath(ch Channel, path string) string {
	base := strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
	if ch.Type == channelTypeOpenAI && strings.HasPrefix(base, cloudflareGatewayPrefix) {
		path = strings.TrimPrefix(path, "/v1")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// resolveURL resolves a provider-supplied URL, absolute or relative, against
// the channel base URL and refuses anything that is not plain http(s).
func resolveURL(baseURL string, candidate string) (string, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("%w: invalid video channel base url", ErrConfiguration)
	}
	target, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil {
		return "", fmt.Errorf("invalid video provider url: %w", err)
	}
	if !target.IsAbs() {
		if !strings.HasPrefix(target.Path, "/") {
			base.Path = strings.TrimRight(base.Path, "/") + "/"
		}
		target = base.ResolveReference(target)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", fmt.Errorf("unsupported video provider url scheme")
	}
	if target.Host == "" || target.User != nil {
		return "", fmt.Errorf("invalid video provider url")
	}
	return target.String(), nil
}

// renderTemplate fills {id} in a URL template and resolves it against the base.
func renderTemplate(baseURL string, template string, upstreamID string) (string, error) {
	if !strings.Contains(template, "{id}") {
		return "", fmt.Errorf("%w: video url template must contain {id}", ErrConfiguration)
	}
	return resolveURL(baseURL, strings.ReplaceAll(template, "{id}", url.PathEscape(strings.TrimSpace(upstreamID))))
}

// SameOrigin reports whether two URLs share scheme, host and effective port.
func SameOrigin(left string, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) &&
		effectivePort(leftURL) == effectivePort(rightURL)
}

func effectivePort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}

// bearerHeaders is the common authentication: a bearer key plus the
// channel's header overrides with {api_key} expanded.
func bearerHeaders(ch Channel) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+ch.Key)
	applyHeaderOverride(header, ch)
	return header
}

func applyHeaderOverride(header http.Header, ch Channel) {
	for name, value := range ch.HeaderOverride {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		header.Set(name, strings.ReplaceAll(text, "{api_key}", ch.Key))
	}
}

func jsonHeaders(header http.Header) http.Header {
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	return header
}
