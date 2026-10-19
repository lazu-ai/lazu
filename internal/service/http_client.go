package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/setting/system_setting"

	"golang.org/x/net/proxy"
)

var (
	httpClient            *http.Client
	streamHTTPClient      *http.Client
	relayHTTPClient       *http.Client
	relayStreamHTTPClient *http.Client
	internalHTTPClient    *http.Client
	proxyClientLock       sync.Mutex
	proxyClients          = make(map[string]*http.Client)
	streamingProxyClients = make(map[string]*http.Client)
)

func checkRedirect(req *http.Request, via []*http.Request) error {
	fetchSetting := system_setting.GetFetchSetting()
	urlStr := req.URL.String()
	if err := common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", urlStr, err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func checkRelayRedirect(_ *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	return nil
}

func relayTimeoutDuration() time.Duration {
	return time.Duration(common.NormalizeRelayTimeoutSeconds(common.RelayTimeout)) * time.Second
}

func managedHTTPClient() *http.Client {
	if httpClient == nil {
		InitHttpClient()
	}
	return httpClient
}

func managedStreamingHTTPClient() *http.Client {
	if streamHTTPClient == nil {
		InitHttpClient()
	}
	return streamHTTPClient
}

func managedRelayHTTPClient() *http.Client {
	if relayHTTPClient == nil {
		InitHttpClient()
	}
	return relayHTTPClient
}

func managedRelayStreamingHTTPClient() *http.Client {
	if relayStreamHTTPClient == nil {
		InitHttpClient()
	}
	return relayStreamHTTPClient
}

func managedInternalHTTPClient() *http.Client {
	if internalHTTPClient == nil {
		InitHttpClient()
	}
	return internalHTTPClient
}

func InitHttpClient() {
	httpClient = &http.Client{
		Transport:     directTransport(),
		Timeout:       relayTimeoutDuration(),
		CheckRedirect: checkRedirect,
	}
	streamHTTPClient = &http.Client{
		Transport:     directTransport(),
		CheckRedirect: checkRedirect,
	}
	relayHTTPClient = &http.Client{
		Transport:     relayDirectTransport(),
		Timeout:       relayTimeoutDuration(),
		CheckRedirect: checkRelayRedirect,
	}
	relayStreamHTTPClient = &http.Client{
		Transport:     relayDirectTransport(),
		CheckRedirect: checkRelayRedirect,
	}
	internalHTTPClient = &http.Client{
		Transport: internalTransport(),
		Timeout:   relayTimeoutDuration(),
	}
}

func GetHttpClient() *http.Client {
	return managedHTTPClient()
}

func GetStreamingHttpClient() *http.Client {
	return managedStreamingHTTPClient()
}

func GetRelayHttpClient() *http.Client {
	return managedRelayHTTPClient()
}

func GetRelayStreamingHttpClient() *http.Client {
	return managedRelayStreamingHTTPClient()
}

func GetInternalHttpClient() *http.Client {
	return managedInternalHTTPClient()
}

// ResetProxyClientCache 清空代理客户端缓存，确保下次使用时重新初始化
func ResetProxyClientCache() {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	for _, client := range proxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	for _, client := range streamingProxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	proxyClients = make(map[string]*http.Client)
	streamingProxyClients = make(map[string]*http.Client)
}

// NewProxyHttpClient 创建支持代理的 HTTP 客户端
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	return newProxyHTTPClient(proxyURL, false)
}

// NewStreamingProxyHttpClient creates a client for streaming relay responses.
// It keeps connection/header timeouts but intentionally avoids http.Client's
// whole-body Timeout so long-running SSE responses are not cut off.
func NewStreamingProxyHttpClient(proxyURL string) (*http.Client, error) {
	return newProxyHTTPClient(proxyURL, true)
}

func newProxyHTTPClient(proxyURL string, streaming bool) (*http.Client, error) {
	if proxyURL == "" {
		if streaming {
			return managedRelayStreamingHTTPClient(), nil
		}
		return managedRelayHTTPClient(), nil
	}

	proxyClientLock.Lock()
	cache := proxyClients
	if streaming {
		cache = streamingProxyClients
	}
	if client, ok := cache[proxyURL]; ok {
		proxyClientLock.Unlock()
		return client, nil
	}
	proxyClientLock.Unlock()

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}

	switch parsedURL.Scheme {
	case "http", "https":
		client := &http.Client{
			Transport: relayTransport(&net.Dialer{
				Timeout:   relayTimeoutDuration(),
				KeepAlive: 30 * time.Second,
			}, false, http.ProxyURL(parsedURL)),
			CheckRedirect: checkRelayRedirect,
		}
		if !streaming {
			client.Timeout = relayTimeoutDuration()
		}
		return registerProxyClient(proxyURL, streaming, client), nil

	case "socks5", "socks5h":
		// 获取认证信息
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{
				User:     parsedURL.User.Username(),
				Password: "",
			}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}

		// 创建 SOCKS5 代理拨号器
		// proxy.SOCKS5 使用 tcp 参数，所有 TCP 连接包括 DNS 查询都将通过代理进行。行为与 socks5h 相同
		dialTimeout := relayTimeoutDuration()
		forwardDialer := &net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: 30 * time.Second,
		}
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, forwardDialer)
		if err != nil {
			return nil, err
		}

		client := &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialProxyWithContext(ctx, dialer, network, addr, dialTimeout)
				},
				MaxIdleConns:          relayMaxIdleConns(),
				MaxIdleConnsPerHost:   relayMaxIdleConnsPerHost(),
				ForceAttemptHTTP2:     common.GetEnvOrDefaultBool("RELAY_HTTP2_ENABLED", true),
				ResponseHeaderTimeout: dialTimeout,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
			CheckRedirect: checkRelayRedirect,
			Timeout:       proxyClientTimeout(streaming, dialTimeout),
		}
		return registerProxyClient(proxyURL, streaming, client), nil

	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
}

func proxyClientTimeout(streaming bool, timeout time.Duration) time.Duration {
	if streaming {
		return 0
	}
	return timeout
}

func dialProxyWithContext(ctx context.Context, dialer proxy.Dialer, network, addr string, timeout time.Duration) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok && timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, network, addr)
	}
	return dialProxyWithoutNativeContext(ctx, dialer, network, addr)
}

func dialProxyWithoutNativeContext(ctx context.Context, dialer proxy.Dialer, network, addr string) (net.Conn, error) {
	type dialResult struct {
		conn net.Conn
		err  error
	}
	result := make(chan dialResult, 1)
	go func() {
		conn, err := dialer.Dial(network, addr)
		if conn != nil && ctx.Err() != nil {
			_ = conn.Close()
		}
		result <- dialResult{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-result:
		return res.conn, res.err
	}
}

type proxyAwareProtectedTransport struct {
	direct  *http.Transport
	proxy   *http.Transport
	proxyFn func(*http.Request) (*url.URL, error)
}

func (t *proxyAwareProtectedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	proxyURL, err := t.proxyFn(req)
	if err != nil {
		return nil, err
	}
	if proxyURL != nil {
		return t.proxy.RoundTrip(req)
	}
	return t.direct.RoundTrip(req)
}

func (t *proxyAwareProtectedTransport) CloseIdleConnections() {
	t.direct.CloseIdleConnections()
	t.proxy.CloseIdleConnections()
}

func directTransport() http.RoundTripper {
	return directTransportWithProxy(http.ProxyFromEnvironment)
}

func directTransportWithProxy(proxyFn func(*http.Request) (*url.URL, error)) http.RoundTripper {
	dialer := &net.Dialer{
		Timeout:   relayTimeoutDuration(),
		KeepAlive: 30 * time.Second,
	}
	return &proxyAwareProtectedTransport{
		direct:  relayTransport(dialer, true, noProxy),
		proxy:   relayTransport(dialer, false, proxyFn),
		proxyFn: proxyFn,
	}
}

func relayDirectTransport() *http.Transport {
	return relayTransport(&net.Dialer{
		Timeout:   relayTimeoutDuration(),
		KeepAlive: 30 * time.Second,
	}, false, nil)
}

func internalTransport() *http.Transport {
	return relayTransport(&net.Dialer{
		Timeout:   relayTimeoutDuration(),
		KeepAlive: 30 * time.Second,
	}, false, noProxy)
}

func relayTransport(dialer *net.Dialer, protectTarget bool, proxyFn func(*http.Request) (*url.URL, error)) *http.Transport {
	if proxyFn == nil {
		proxyFn = http.ProxyFromEnvironment
	}
	transport := &http.Transport{
		Proxy:                 proxyFn,
		MaxIdleConns:          relayMaxIdleConns(),
		MaxIdleConnsPerHost:   relayMaxIdleConnsPerHost(),
		ForceAttemptHTTP2:     common.GetEnvOrDefaultBool("RELAY_HTTP2_ENABLED", true),
		ResponseHeaderTimeout: relayTimeoutDuration(),
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if protectTarget {
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			fetchSetting := system_setting.GetFetchSetting()
			if !fetchSetting.EnableSSRFProtection {
				return dialer.DialContext(ctx, network, addr)
			}
			protection, err := common.NewSSRFProtectionFromFetchSetting(
				fetchSetting.AllowPrivateIp,
				fetchSetting.DomainFilterMode,
				fetchSetting.IpFilterMode,
				fetchSetting.DomainList,
				fetchSetting.IpList,
				fetchSetting.AllowedPorts,
				fetchSetting.ApplyIPFilterForDomain,
			)
			if err != nil {
				return nil, err
			}
			return protection.DialContext(ctx, network, addr, dialer)
		}
	} else {
		transport.DialContext = dialer.DialContext
	}
	return transport
}

func noProxy(*http.Request) (*url.URL, error) {
	return nil, nil
}

// Internal tuning overrides never make the default connection pool unbounded.
func relayMaxIdleConns() int {
	value := common.GetEnvOrDefault("RELAY_MAX_IDLE_CONNS", 500)
	if value < 1 {
		return 500
	}
	return value
}
func relayMaxIdleConnsPerHost() int {
	value := common.GetEnvOrDefault("RELAY_MAX_IDLE_CONNS_PER_HOST", 100)
	if value < 1 {
		return 100
	}
	return value
}

// Concurrent cold requests must converge on one transport. Creating multiple
// clients and overwriting the cache throws away their newly opened idle pools.
func registerProxyClient(proxyURL string, streaming bool, client *http.Client) *http.Client {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	cache := proxyClients
	if streaming {
		cache = streamingProxyClients
	}
	if existing, ok := cache[proxyURL]; ok {
		client.CloseIdleConnections()
		return existing
	}
	cache[proxyURL] = client
	return client
}
