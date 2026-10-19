package service

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRelayTransportPerformanceDefaultsAndOverrides(t *testing.T) {
	t.Setenv("RELAY_MAX_IDLE_CONNS", "")
	t.Setenv("RELAY_MAX_IDLE_CONNS_PER_HOST", "")
	t.Setenv("RELAY_HTTP2_ENABLED", "")
	transport := relayDirectTransport()
	if transport.MaxIdleConns != 500 || transport.MaxIdleConnsPerHost != 100 || !transport.ForceAttemptHTTP2 {
		t.Fatalf("transport pool/h2: %+v", transport)
	}
	t.Setenv("RELAY_MAX_IDLE_CONNS", "900")
	t.Setenv("RELAY_MAX_IDLE_CONNS_PER_HOST", "125")
	t.Setenv("RELAY_HTTP2_ENABLED", "false")
	transport = internalTransport()
	if transport.MaxIdleConns != 900 || transport.MaxIdleConnsPerHost != 125 || transport.ForceAttemptHTTP2 {
		t.Fatal("overrides not applied")
	}
	t.Setenv("RELAY_MAX_IDLE_CONNS", "-1")
	t.Setenv("RELAY_MAX_IDLE_CONNS_PER_HOST", "0")
	if relayMaxIdleConns() != 500 || relayMaxIdleConnsPerHost() != 100 {
		t.Fatal("invalid overrides permit unbounded/empty idle pool")
	}
}

func TestConcurrentProxyClientCreationReusesOneTransport(t *testing.T) {
	ResetProxyClientCache()
	defer ResetProxyClientCache()
	clients := make(chan *http.Client, 32)
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			client, err := NewStreamingProxyHttpClient("socks5://127.0.0.1:1080")
			if err != nil {
				t.Error(err)
				return
			}
			clients <- client
		}()
	}
	workers.Wait()
	close(clients)
	var first *http.Client
	for client := range clients {
		if first == nil {
			first = client
		}
		if client != first {
			t.Fatal("concurrent cache misses created separate connection pools")
		}
	}
	transport := first.Transport.(*http.Transport)
	if !transport.ForceAttemptHTTP2 || transport.MaxIdleConnsPerHost != 100 || transport.MaxIdleConns != 500 {
		t.Fatalf("SOCKS transport: %+v", transport)
	}
}

func TestHTTP2ConcurrentRelayReusesConnections(t *testing.T) {
	var fresh atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("upstream protocol %s; HTTP/2 not attempted", r.Proto)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			fresh.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	transport := relayDirectTransport()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Local TLS fixture only.
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	wave := func(count int) {
		var workers sync.WaitGroup
		for i := 0; i < count; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				resp, err := client.Get(server.URL)
				if err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}()
		}
		workers.Wait()
	}
	wave(32)
	before := fresh.Load()
	for i := 0; i < 20; i++ {
		wave(32)
	}
	if ratio := float64(fresh.Load()-before) / 640; ratio >= 0.05 {
		t.Fatalf("warm 32-way relay new connection ratio %.3f", ratio)
	}
}
