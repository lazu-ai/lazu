package service

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

// VideoUpstreamClient returns the HTTP client for one video provider call.
// An authenticated call carries the channel key and uses the relay client,
// whose redirect policy keeps it on its own origin. An unauthenticated one is
// an external artifact URL (often a signed CDN link that redirects to another
// host): it goes through the SSRF filter before the call and on every
// redirect, and may change origin. The channel proxy applies to both.
func VideoUpstreamClient(proxyURL string, requestURL string, authenticated bool) (*http.Client, error) {
	if !authenticated {
		if err := ValidateExternalVideoArtifactURL(requestURL); err != nil {
			return nil, err
		}
	}
	proxyURL = strings.TrimSpace(proxyURL)
	var client *http.Client
	if proxyURL == "" {
		if authenticated {
			return GetRelayHttpClient(), nil
		}
		client = GetHttpClient()
	} else {
		var err error
		client, err = NewProxyHttpClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("configure video channel proxy: %w", err)
		}
		if authenticated {
			return client, nil
		}
	}
	external := *client
	external.CheckRedirect = checkExternalVideoRedirect
	return &external, nil
}

func checkExternalVideoRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxFetchRedirects {
		return fmt.Errorf("stopped after %d redirects", maxFetchRedirects)
	}
	return ValidateExternalVideoArtifactURL(req.URL.String())
}

// ValidateExternalVideoArtifactURL applies the operator's fetch policy
// (SSRF protection, private IPs, domain and port lists) to a provider URL the
// gateway did not configure itself.
func ValidateExternalVideoArtifactURL(rawURL string) error {
	fetchSetting := system_setting.GetFetchSetting()
	if err := fetchSetting.ValidateURL(rawURL); err != nil {
		return fmt.Errorf("external video artifact url blocked: %w", err)
	}
	return nil
}
