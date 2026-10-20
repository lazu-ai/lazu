package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lazu-ai/lazu/internal/setting/system_setting"
	"github.com/lazu-ai/lazu/internal/types"
)

// withFetchPolicy installs a fetch policy for the test and restores the
// previous one. Ports are left unrestricted so httptest's random port is not
// what refuses the request; the private-address rule is what is under test.
func withFetchPolicy(t *testing.T, mutate func(*system_setting.FetchSetting)) {
	t.Helper()
	withRelayTimeout(t, 5)
	fetchSetting := system_setting.GetFetchSetting()
	previous := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.DomainList = []string{}
	fetchSetting.IpFilterMode = false
	fetchSetting.IpList = []string{}
	fetchSetting.AllowedPorts = []string{}
	fetchSetting.ApplyIPFilterForDomain = false
	fetchSetting.PrivateAllowList = []string{}
	if mutate != nil {
		mutate(fetchSetting)
	}
	t.Cleanup(func() {
		*fetchSetting = previous
	})
}

func requireRefusedAsPrivate(t *testing.T, err error) *types.LazuError {
	t.Helper()
	if err == nil {
		t.Fatal("expected the download to be refused")
	}
	var lazuErr *types.LazuError
	if !errors.As(err, &lazuErr) {
		t.Fatalf("expected a *types.LazuError, got %T: %v", err, err)
	}
	if lazuErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", lazuErr.StatusCode)
	}
	if !types.IsSkipRetryError(lazuErr) {
		t.Fatal("a fetch-policy refusal must not be retried on another channel")
	}
	wire := lazuErr.ToOpenAIError()
	if wire.Code != string(types.ErrorCodeInvalidRequest) {
		t.Fatalf("code = %q, want %q", wire.Code, types.ErrorCodeInvalidRequest)
	}
	if !strings.Contains(wire.Message, "private or internal network address") {
		t.Fatalf("client message does not name the reason: %q", wire.Message)
	}
	if strings.Contains(wire.Message, "127.0.0.1") || strings.Contains(wire.Message, "169.254") {
		t.Fatalf("client message echoes an address: %q", wire.Message)
	}
	return lazuErr
}

func TestDoDownloadRequestRefusesLoopbackByDefault(t *testing.T) {
	var served atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	}))
	defer server.Close()
	withFetchPolicy(t, nil)

	resp, err := DoDownloadRequest(server.URL+"/a.png", "test")
	if resp != nil {
		_ = resp.Body.Close()
	}
	requireRefusedAsPrivate(t, err)
	if served.Load() != 0 {
		t.Fatal("the loopback server must not be reached")
	}
}

func TestDoDownloadRequestAllowsAllowlistedLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-bytes"))
	}))
	defer server.Close()
	withFetchPolicy(t, func(s *system_setting.FetchSetting) {
		s.PrivateAllowList = []string{"127.0.0.0/8"}
	})

	resp, err := DoDownloadRequest(server.URL+"/a.png", "test")
	if err != nil {
		t.Fatalf("expected allowlisted loopback to be fetched, got %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "png-bytes" {
		t.Fatalf("unexpected response %d %q", resp.StatusCode, body)
	}
}

func TestDoDownloadRequestRefusesRedirectToPrivateAddress(t *testing.T) {
	var privateServed atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			// The first hop is allowed (by host); it redirects to the same
			// listener by its literal loopback address, which is not.
			http.Redirect(w, r, "http://"+server.Listener.Addr().String()+"/private", http.StatusFound)
		case "/metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/private":
			privateServed.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("secret"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	withFetchPolicy(t, func(s *system_setting.FetchSetting) {
		s.PrivateAllowList = []string{"localhost"}
	})
	port := server.URL[strings.LastIndex(server.URL, ":"):]

	for _, path := range []string{"/start", "/metadata"} {
		resp, err := DoDownloadRequest("http://localhost"+port+path, "test")
		if resp != nil {
			_ = resp.Body.Close()
		}
		requireRefusedAsPrivate(t, err)
	}
	if privateServed.Load() != 0 {
		t.Fatal("the private redirect target must never be fetched")
	}
}

func TestDoDownloadRequestStopsAfterFiveRedirects(t *testing.T) {
	var hops atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer server.Close()
	withFetchPolicy(t, func(s *system_setting.FetchSetting) {
		s.PrivateAllowList = []string{"127.0.0.0/8"}
	})

	resp, err := DoDownloadRequest(server.URL+"/again", "test")
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "stopped after 5 redirects") {
		t.Fatalf("expected the redirect cap to stop the chain, got %v", err)
	}
	if hops.Load() > 6 {
		t.Fatalf("followed %d hops, want at most 6", hops.Load())
	}
}

func TestDoDownloadRequestRefusesNonHTTPScheme(t *testing.T) {
	withFetchPolicy(t, nil)
	_, err := DoDownloadRequest("ftp://example.com/a.png", "test")
	var lazuErr *types.LazuError
	if !errors.As(err, &lazuErr) || lazuErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected a 400 for a non-http scheme, got %v", err)
	}
	if msg := lazuErr.ToOpenAIError().Message; !strings.Contains(msg, "http or https") {
		t.Fatalf("client message does not name the reason: %q", msg)
	}
}

// The converters wrap the refusal with %w and the handlers then wrap that
// as a conversion failure. The nested code, status, reason and skip-retry
// must survive both layers, or the client gets the generic template.
func TestRefusalSurvivesConverterAndHandlerWrapping(t *testing.T) {
	withFetchPolicy(t, nil)
	_, err := DoDownloadRequest("http://127.0.0.1/a.png", "test")
	if err == nil {
		t.Fatal("expected refusal")
	}
	converter := fmt.Errorf("get file base64 from url failed: %w", err)
	handler := types.NewError(converter, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	requireRefusedAsPrivate(t, handler)
}
