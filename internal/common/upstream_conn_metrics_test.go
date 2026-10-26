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
