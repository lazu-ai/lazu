package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/service"
)

// Fetching the model list for a channel that is not saved yet uses the relay
// client: it reaches a loopback upstream as before, and it gives up after the
// relay timeout instead of waiting forever.
func TestNewChannelModelListUsesTheRelayClient(t *testing.T) {
	previous := common.RelayTimeout
	t.Cleanup(func() {
		common.RelayTimeout = previous
		service.InitHttpClient()
	})
	common.RelayTimeout = 1
	service.InitHttpClient()

	answering := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer answering.Close()
	body, err := getResponseBodyWithoutChannel(context.Background(), http.MethodGet, answering.URL+"/v1/models", http.Header{})
	if err != nil || string(body) != `{"data":[{"id":"model-a"}]}` {
		t.Fatalf("loopback upstream: body %q, err %v", body, err)
	}

	release := make(chan struct{})
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer silent.Close()
	defer close(release)
	start := time.Now()
	if _, err := getResponseBodyWithoutChannel(context.Background(), http.MethodGet, silent.URL+"/v1/models", http.Header{}); err == nil {
		t.Fatal("an upstream that never answers must fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("gave up after %s, want about the 1s relay timeout", elapsed)
	}
}

// The model list gets its own bound, well under the relay timeout a
// generation needs: an upstream that never answers fails the form's fetch
// after it, not after RELAY_TIMEOUT.
func TestUpstreamModelListGivesUpBeforeTheRelayTimeout(t *testing.T) {
	previousRelay, previousList := common.RelayTimeout, upstreamModelListTimeout
	t.Cleanup(func() {
		common.RelayTimeout, upstreamModelListTimeout = previousRelay, previousList
		service.InitHttpClient()
	})
	common.RelayTimeout = 600
	service.InitHttpClient()
	upstreamModelListTimeout = 300 * time.Millisecond

	release := make(chan struct{})
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer silent.Close()
	defer close(release)
	start := time.Now()
	if _, err := fetchUpstreamModelIDs(silent.URL, constant.ChannelTypeOpenAI, "k", nil); err == nil {
		t.Fatal("an upstream that never answers must fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("gave up after %s, want about the model-list bound", elapsed)
	}
}
