package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"
)

func withRelayTimeout(t *testing.T, seconds int) {
	t.Helper()
	oldTimeout := common.RelayTimeout
	oldClient := httpClient
	oldStreamClient := streamHTTPClient
	oldRelayClient := relayHTTPClient
	oldRelayStreamClient := relayStreamHTTPClient
	oldInternalClient := internalHTTPClient
	oldProxyClients := proxyClients
	oldStreamingProxyClients := streamingProxyClients
	common.RelayTimeout = seconds
	httpClient = nil
	streamHTTPClient = nil
	relayHTTPClient = nil
	relayStreamHTTPClient = nil
	internalHTTPClient = nil
	proxyClients = make(map[string]*http.Client)
	streamingProxyClients = make(map[string]*http.Client)
	t.Cleanup(func() {
		common.RelayTimeout = oldTimeout
		httpClient = oldClient
		streamHTTPClient = oldStreamClient
		relayHTTPClient = oldRelayClient
		relayStreamHTTPClient = oldRelayStreamClient
		internalHTTPClient = oldInternalClient
		proxyClients = oldProxyClients
		streamingProxyClients = oldStreamingProxyClients
	})
}

func TestInitHttpClientAppliesDefaultTimeoutWhenRelayTimeoutZero(t *testing.T) {
	withRelayTimeout(t, 0)

	InitHttpClient()

	if got := GetHttpClient().Timeout; got != time.Duration(common.DefaultRelayTimeoutSeconds)*time.Second {
		t.Fatalf("expected default timeout %s, got %s", time.Duration(common.DefaultRelayTimeoutSeconds)*time.Second, got)
	}
}

func TestInitHttpClientUsesConfiguredRelayTimeout(t *testing.T) {
	withRelayTimeout(t, 42)

	InitHttpClient()

	if got := GetHttpClient().Timeout; got != 42*time.Second {
		t.Fatalf("expected configured timeout 42s, got %s", got)
	}
	if got := GetInternalHttpClient().Timeout; got != 42*time.Second {
		t.Fatalf("expected internal client timeout 42s, got %s", got)
	}
}

func TestNewProxyHttpClientWithoutProxyUsesManagedTimeout(t *testing.T) {
	withRelayTimeout(t, 0)

	client, err := NewProxyHttpClient("")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := client.Timeout; got != time.Duration(common.DefaultRelayTimeoutSeconds)*time.Second {
		t.Fatalf("expected default timeout %s, got %s", time.Duration(common.DefaultRelayTimeoutSeconds)*time.Second, got)
	}
}

func TestRelayClientWithoutProxyPreservesEnvironmentProxyHook(t *testing.T) {
	withRelayTimeout(t, 5)

	InitHttpClient()

	transport, ok := GetRelayHttpClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected relay transport, got %T", GetRelayHttpClient().Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("expected relay client to preserve the default environment proxy hook")
	}
}

func TestRelayClientWithoutProxyDoesNotInheritFetchSSRFDefaults(t *testing.T) {
	withRelayTimeout(t, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.AllowedPorts = []string{"80", "443", "8080", "8443"}
	t.Cleanup(func() {
		*fetchSetting = previousFetchSetting
	})

	if resp, err := GetHttpClient().Get(server.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected fetch client to block private random-port URL")
	}

	client, err := NewProxyHttpClient("")
	if err != nil {
		t.Fatalf("expected relay client without proxy: %v", err)
	}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("expected relay client to allow admin-configured private upstream: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestFetchClientWithProxyDoesNotSSRFCheckProxyDial(t *testing.T) {
	withRelayTimeout(t, 5)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" {
			t.Fatalf("expected proxy to receive request for example.com, got host %q", r.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxyServer.Close()
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}

	fetchSetting := system_setting.GetFetchSetting()
	previousFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.AllowedPorts = []string{"80", "443"}
	t.Cleanup(func() {
		*fetchSetting = previousFetchSetting
	})

	client := &http.Client{
		Transport:     directTransportWithProxy(http.ProxyURL(proxyURL)),
		CheckRedirect: checkRedirect,
		Timeout:       relayTimeoutDuration(),
	}

	resp, err := client.Get("http://example.com/proxied")
	if err != nil {
		t.Fatalf("expected proxy dial to bypass target SSRF checks on the proxy address: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected proxy response 204, got %d", resp.StatusCode)
	}
}

func TestStreamingHttpClientHasNoWholeBodyTimeout(t *testing.T) {
	withRelayTimeout(t, 42)

	InitHttpClient()

	if got := GetHttpClient().Timeout; got != 42*time.Second {
		t.Fatalf("expected regular client timeout 42s, got %s", got)
	}
	if got := GetStreamingHttpClient().Timeout; got != 0 {
		t.Fatalf("expected streaming client to avoid whole-body timeout, got %s", got)
	}
}

func TestNewStreamingProxyHttpClientWithoutProxyHasNoWholeBodyTimeout(t *testing.T) {
	withRelayTimeout(t, 42)

	client, err := NewStreamingProxyHttpClient("")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := client.Timeout; got != 0 {
		t.Fatalf("expected streaming proxy client to avoid whole-body timeout, got %s", got)
	}
}

type blockingContextDialer struct{}

func (blockingContextDialer) Dial(string, string) (net.Conn, error) {
	return nil, errors.New("Dial should not be called when DialContext is available")
}

func (blockingContextDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDialProxyWithContextAppliesFallbackDeadline(t *testing.T) {
	start := time.Now()
	conn, err := dialProxyWithContext(context.Background(), blockingContextDialer{}, "tcp", "example.com:443", 50*time.Millisecond)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("expected fallback deadline error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected fallback deadline to stop stalled proxy dial quickly, took %s", elapsed)
	}
}

func TestNewStreamingSocksProxyClientHonorsDialContextDeadline(t *testing.T) {
	withRelayTimeout(t, 42)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	t.Cleanup(func() {
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})

	client, err := NewStreamingProxyHttpClient("socks5://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("expected streaming socks client: %v", err)
	}
	if got := client.Timeout; got != 0 {
		t.Fatalf("expected streaming socks client to avoid whole-body timeout, got %s", got)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected http transport, got %T", client.Transport)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	conn, err := transport.DialContext(ctx, "tcp", "example.com:443")
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("expected SOCKS handshake to fail when the dial context expires")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected context deadline to stop stalled SOCKS dial quickly, took %s", elapsed)
	}
}
