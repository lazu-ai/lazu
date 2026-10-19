package controller

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestSelfAccessAddressesOfferOnlyAPIHosts(t *testing.T) {
	model.ApplyAccessAddressesForTest(`[{"url":"https://api.example.cn","kind":"cn_direct","user_ids":[7]},{"url":"https://example.cn","kind":"cn_direct","user_ids":[7]}]`)
	t.Cleanup(func() { model.ApplyAccessAddressesForTest("") })
	got := selfAccessAddresses(7)
	if len(got) != 1 || got[0].URL != "https://api.example.cn" {
		t.Fatalf("self addresses = %+v, want only the api host", got)
	}
	if registered, allowed := model.AccessAddressAllowed("example.cn", 8); !registered || allowed {
		t.Fatal("the hidden web domain must still be gated")
	}
}
