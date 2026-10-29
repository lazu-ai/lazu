package dto

import (
	"testing"

	"github.com/lazu-ai/lazu/internal/common"
)

// Token-id inputs are counted one token per id; the text estimate never saw
// them, so an upstream without usage billed them as nothing.
func TestEmbeddingRequestCountsTokenIDInputs(t *testing.T) {
	cases := map[string]int{
		`{"model":"m","input":"plain text"}`:            0,
		`{"model":"m","input":["a","b"]}`:               0,
		`{"model":"m","input":[101,2023,2003,102]}`:     4,
		`{"model":"m","input":[[101,102],[7,8,9],"x"]}`: 5,
	}
	for body, want := range cases {
		var request EmbeddingRequest
		if err := common.Unmarshal([]byte(body), &request); err != nil {
			t.Fatal(err)
		}
		if got := request.GetTokenCountMeta().TokenIDCount; got != want {
			t.Fatalf("%s: token ids = %d, want %d", body, got, want)
		}
	}
}
