package service

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

// VideoUpstreamClient returns the HTTP client for one video provider call.
// An authenticated call carries the channel key and may only follow
// redirects on its own origin; an unauthenticated one is an external
// artifact URL and goes through the SSRF filter, on every redirect too. The
// channel proxy applies to both.
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
			client = GetRelayHttpClient()
		} else {
			client = GetHttpClient()
		}
	} else {
		var err error
		client, err = NewProxyHttpClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("configure video channel proxy: %w", err)
		}
	}
	secured := *client
	previousRedirectCheck := client.CheckRedirect
	secured.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if authenticated {
			if len(via) > 0 && !sameVideoOrigin(requestURL, request.URL.String()) {
				return fmt.Errorf("authenticated video artifact redirect changed origin")
			}
		} else if err := ValidateExternalVideoArtifactURL(request.URL.String()); err != nil {
			return err
		}
		if previousRedirectCheck != nil {
			return previousRedirectCheck(request, via)
		}
		return nil
	}
	return &secured, nil
}

// ValidateExternalVideoArtifactURL applies the operator's fetch policy
// (SSRF protection, private IPs, domain and port lists) to a provider URL the
// gateway did not configure itself.
func ValidateExternalVideoArtifactURL(rawURL string) error {
	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(
		rawURL,
		fetchSetting.EnableSSRFProtection,
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	); err != nil {
		return fmt.Errorf("external video artifact url blocked: %w", err)
	}
	return nil
}

func sameVideoOrigin(left string, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	port := func(value *url.URL) string {
		if value.Port() != "" {
			return value.Port()
		}
		if strings.EqualFold(value.Scheme, "https") {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) &&
		port(leftURL) == port(rightURL)
}
