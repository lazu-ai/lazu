package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

// recordingTransport answers every request in memory, so redirects between
// unrelated hostnames can be exercised through a real client's redirect
// policy without any network. A request to a host in redirects gets that
// status and Location; any other host answers 200.
type recordingTransport struct {
	mu        sync.Mutex
	redirects map[string]redirectAnswer
	received  []recordedRequest
}

type redirectAnswer struct {
	status   int
	location string
}

type recordedRequest struct {
	url    string
	header http.Header
	body   string
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := ""
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		body = string(raw)
	}
	t.mu.Lock()
	t.received = append(t.received, recordedRequest{url: req.URL.String(), header: req.Header.Clone(), body: body})
	answer, redirect := t.redirects[req.URL.Scheme+"://"+req.URL.Host]
	t.mu.Unlock()
	header := http.Header{}
	status := http.StatusOK
	if redirect {
		status = answer.status
		header.Set("Location", answer.location)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func (t *recordingTransport) requestsTo(prefix string) []recordedRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []recordedRequest
	for _, r := range t.received {
		if strings.HasPrefix(r.url, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// credentialedClients is every client that sends a channel or worker key.
func credentialedClients(t *testing.T) map[string]*http.Client {
	t.Helper()
	withRelayTimeout(t, 5)
	InitHttpClient()
	httpProxy, err := NewProxyHttpClient("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	socksProxy, err := NewStreamingProxyHttpClient("socks5://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]*http.Client{
		"relay":                  GetRelayHttpClient(),
		"relay streaming":        GetRelayStreamingHttpClient(),
		"relay via http proxy":   httpProxy,
		"relay via socks5 proxy": socksProxy,
		"internal service":       GetInternalHttpClient(),
	}
}

func withTransport(client *http.Client, transport http.RoundTripper) *http.Client {
	copied := *client
	copied.Transport = transport
	return &copied
}

func vendorKeyRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(`{"prompt":"synthetic"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", "synthetic-anthropic-key")
	req.Header.Set("x-goog-api-key", "synthetic-gemini-key")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCredentialedClientsRefuseRedirectsThatLeaveTheOrigin(t *testing.T) {
	cases := []struct {
		name     string
		start    string
		status   int
		location string
		leaked   string
	}{
		{"another host, 307 resends body", "https://one.invalid/v1/messages", http.StatusTemporaryRedirect, "https://two.invalid/v1/messages", "https://two.invalid"},
		{"another host, 308", "https://one.invalid/v1/messages", http.StatusPermanentRedirect, "https://two.invalid/v1/messages", "https://two.invalid"},
		{"another host, 302", "https://one.invalid/v1/models", http.StatusFound, "https://two.invalid/v1/models", "https://two.invalid"},
		{"https to http on the same host", "https://one.invalid/v1/messages", http.StatusTemporaryRedirect, "http://one.invalid/v1/messages", "http://one.invalid"},
		{"another port on the same host", "https://one.invalid/v1/messages", http.StatusTemporaryRedirect, "https://one.invalid:8443/v1/messages", "https://one.invalid:8443"},
	}
	for clientName, client := range credentialedClients(t) {
		for _, tc := range cases {
			t.Run(clientName+"/"+tc.name, func(t *testing.T) {
				transport := &recordingTransport{redirects: map[string]redirectAnswer{
					"https://one.invalid": {status: tc.status, location: tc.location},
				}}
				resp, err := withTransport(client, transport).Do(vendorKeyRequest(t, tc.start))
				if resp != nil {
					_ = resp.Body.Close()
				}
				if !errors.Is(err, ErrRedirectLeftOrigin) {
					t.Fatalf("expected ErrRedirectLeftOrigin, got %v", err)
				}
				// http.Client's *url.Error names the refused URL; the
				// policy's own message names origins only.
				var urlErr *url.Error
				if !errors.As(err, &urlErr) || strings.Contains(urlErr.Err.Error(), "/v1/") {
					t.Fatalf("policy error must name origins only, not paths: %v", err)
				}
				if got := transport.requestsTo(tc.leaked); len(got) != 0 {
					t.Fatalf("redirect target received %d request(s); first carried x-api-key=%q x-goog-api-key=%q body=%q",
						len(got), got[0].header.Get("x-api-key"), got[0].header.Get("x-goog-api-key"), got[0].body)
				}
				if got := len(transport.received); got != 1 {
					t.Fatalf("expected only the original request to be sent, got %d", got)
				}
			})
		}
	}
}

func TestCredentialedClientsFollowRedirectsOnTheSameOrigin(t *testing.T) {
	for clientName, client := range credentialedClients(t) {
		t.Run(clientName, func(t *testing.T) {
			// https://one.invalid and https://ONE.invalid:443 are the same
			// origin: the default port and host case do not matter.
			transport := &recordingTransport{redirects: map[string]redirectAnswer{
				"https://one.invalid": {status: http.StatusTemporaryRedirect, location: "https://ONE.invalid:443/v2/messages"},
			}}
			resp, err := withTransport(client, transport).Do(vendorKeyRequest(t, "https://one.invalid/v1/messages"))
			if err != nil {
				t.Fatalf("same-origin redirect refused: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d want 200", resp.StatusCode)
			}
			followed := transport.requestsTo("https://ONE.invalid:443/v2/messages")
			if len(followed) != 1 {
				t.Fatalf("expected the redirect to be followed once, got %d", len(followed))
			}
			if followed[0].header.Get("x-api-key") == "" || followed[0].body == "" {
				t.Fatal("same-origin 307 must keep the key header and resend the body")
			}
		})
	}
}

func TestCredentialedClientsKeepTheRedirectCap(t *testing.T) {
	client := withTransport(GetRelayHttpClient(), &recordingTransport{redirects: map[string]redirectAnswer{
		"https://one.invalid": {status: http.StatusFound, location: "https://one.invalid/again"},
	}})
	resp, err := client.Get("https://one.invalid/start")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("expected the 10-hop cap, got %v", err)
	}
}

// The same rule over real sockets: two local servers differ by port, so the
// second is another origin and must never see the request.
func TestRelayClientRedirectToAnotherServerSendsItNothing(t *testing.T) {
	withRelayTimeout(t, 5)
	var secondHits int
	var mu sync.Mutex
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		secondHits++
		mu.Unlock()
	}))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/same" {
			if r.Header.Get("x-api-key") == "" {
				w.WriteHeader(http.StatusUnauthorized)
			}
			return
		}
		target := second.URL + "/v1/messages"
		if r.URL.Path == "/hop" {
			target = "/same"
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	}))
	defer first.Close()

	resp, err := GetRelayHttpClient().Do(vendorKeyRequest(t, first.URL+"/hop"))
	if err != nil {
		t.Fatalf("same-server redirect refused: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("same-server redirect lost the key header: status %d", resp.StatusCode)
	}

	resp, err = GetRelayHttpClient().Do(vendorKeyRequest(t, first.URL+"/v1/messages"))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrRedirectLeftOrigin) {
		t.Fatalf("expected ErrRedirectLeftOrigin, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if secondHits != 0 {
		t.Fatalf("second server received %d request(s)", secondHits)
	}
}

func TestVideoClientsApplyTheRedirectRuleOnlyToKeyedCalls(t *testing.T) {
	withRelayTimeout(t, 5)
	fetchSetting := system_setting.GetFetchSetting()
	previous := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	t.Cleanup(func() { *fetchSetting = previous })

	for _, proxyURL := range []string{"", "http://127.0.0.1:1"} {
		redirects := map[string]redirectAnswer{
			"https://provider.invalid": {status: http.StatusFound, location: "https://cdn.invalid/artifact.mp4"},
		}

		keyed, err := VideoUpstreamClient(proxyURL, "https://provider.invalid/v1/videos/x/content", true)
		if err != nil {
			t.Fatal(err)
		}
		transport := &recordingTransport{redirects: redirects}
		resp, err := withTransport(keyed, transport).Do(vendorKeyRequest(t, "https://provider.invalid/v1/videos/x/content"))
		if resp != nil {
			_ = resp.Body.Close()
		}
		if !errors.Is(err, ErrRedirectLeftOrigin) {
			t.Fatalf("proxy %q: keyed video call followed a cross-origin redirect: %v", proxyURL, err)
		}
		if got := transport.requestsTo("https://cdn.invalid"); len(got) != 0 {
			t.Fatalf("proxy %q: redirect target received the keyed request", proxyURL)
		}

		// A signed artifact link carries no key and may land on a CDN.
		external, err := VideoUpstreamClient(proxyURL, "https://provider.invalid/signed/artifact", false)
		if err != nil {
			t.Fatal(err)
		}
		transport = &recordingTransport{redirects: redirects}
		resp, err = withTransport(external, transport).Get("https://provider.invalid/signed/artifact")
		if err != nil {
			t.Fatalf("proxy %q: unauthenticated artifact redirect refused: %v", proxyURL, err)
		}
		_ = resp.Body.Close()
		if got := transport.requestsTo("https://cdn.invalid"); len(got) != 1 {
			t.Fatalf("proxy %q: expected the artifact redirect to be followed, got %d", proxyURL, len(got))
		}
	}
}

func TestExternalVideoRedirectsStayUnderTheFetchPolicy(t *testing.T) {
	withRelayTimeout(t, 5)
	fetchSetting := system_setting.GetFetchSetting()
	previous := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	t.Cleanup(func() { *fetchSetting = previous })

	client, err := VideoUpstreamClient("http://127.0.0.1:1", "https://93.184.215.14/artifact", false)
	if err != nil {
		t.Fatal(err)
	}
	transport := &recordingTransport{redirects: map[string]redirectAnswer{
		"https://93.184.215.14": {status: http.StatusFound, location: "http://127.0.0.1/admin"},
	}}
	resp, err := withTransport(client, transport).Get("https://93.184.215.14/artifact")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected the fetch policy to refuse a redirect to a private address")
	}
	if got := transport.requestsTo("http://127.0.0.1"); len(got) != 0 {
		t.Fatal("refused redirect target was contacted")
	}
}
