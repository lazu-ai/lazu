package common

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
)

func TestUpstreamClientTraceCountsNewAndReusedConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()

	reusedBefore, openedBefore := UpstreamConnCounts()
	timing := &RelayTiming{}
	for range 2 {
		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), UpstreamClientTrace(timing)))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	reused, opened := UpstreamConnCounts()
	if opened-openedBefore != 1 || reused-reusedBefore != 1 {
		t.Fatalf("want one new and one reused connection, got new=%d reused=%d", opened-openedBefore, reused-reusedBefore)
	}
}

func TestUpstreamClientTraceTimesDialAndTLSForNewConnectionsOnly(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	client := server.Client()
	defer client.CloseIdleConnections()

	dialBefore, _ := UpstreamDialDurations.Snapshot()
	tlsBefore, _ := UpstreamTLSHandshakeDurations.Snapshot()
	timing := &RelayTiming{}
	for range 2 {
		req, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), UpstreamClientTrace(timing)))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	dialAfter, _ := UpstreamDialDurations.Snapshot()
	tlsAfter, _ := UpstreamTLSHandshakeDurations.Snapshot()
	last := len(dialAfter) - 1
	if dialAfter[last]-dialBefore[last] != 1 || tlsAfter[last]-tlsBefore[last] != 1 {
		t.Fatalf("want one dial and one handshake for two requests on one connection, got dial=%d tls=%d",
			dialAfter[last]-dialBefore[last], tlsAfter[last]-tlsBefore[last])
	}
}
