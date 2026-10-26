package controller

import (
	"net/http"
	"testing"
)

// The metadata sync client honours HTTP(S)_PROXY like the shared outbound
// client (docs/DEPLOYMENT.md, Outbound connections). net/http reads the
// proxy environment once per process, so the test checks the transport has
// a proxy function rather than setting variables.
func TestModelSyncClientHonoursProxyEnvironment(t *testing.T) {
	transport, ok := newHTTPClient().Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("transport = %#v, want one with a proxy function", newHTTPClient().Transport)
	}
}
